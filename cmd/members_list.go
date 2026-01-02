package cmd

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/ayn2op/discordo/internal/config"
	"github.com/ayn2op/discordo/internal/ui"
	"github.com/ayn2op/tview"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/gdamore/tcell/v3"
)

type membersList struct {
	*tview.List
	cfg *config.Config

	currentGuildID discord.GuildID
	visible        bool

	// Cache for quick lookups: UserID -> list index
	memberItems map[discord.UserID]int
}

type memberItem struct {
	Member   *discord.Member
	Presence *discord.Presence
	RoleInfo *discord.Role // highest colored role
}

func newMembersList(cfg *config.Config) *membersList {
	ml := &membersList{
		List:        tview.NewList(),
		cfg:         cfg,
		visible:     false,
		memberItems: make(map[discord.UserID]int),
	}

	ml.Box = ui.ConfigureBox(ml.Box, &cfg.Theme)
	ml.SetTitle("Members")
	ml.SetInputCapture(ml.onInputCapture)
	ml.ShowSecondaryText(false)
	ml.SetHighlightFullLine(true)

	return ml
}

func (ml *membersList) updateForGuild(guildID discord.GuildID) {
	if !ml.visible {
		return // Don't update if not visible
	}

	ml.currentGuildID = guildID

	// Get cached members
	members, err := discordState.Cabinet.Members(guildID)
	if err != nil || len(members) == 0 {
		// Request from Discord if cache empty
		go func() {
			err := discordState.SendGateway(context.TODO(), &gateway.RequestGuildMembersCommand{
				GuildIDs:  []discord.GuildID{guildID},
				Limit:     0,
				Presences: true,
			})
			if err != nil {
				slog.Error("failed to request guild members", "guild_id", guildID, "err", err)
			}
		}()
	}

	ml.rebuildList()
}

func (ml *membersList) updateMemberPresence(userID discord.UserID) {
	if !ml.visible {
		return
	}

	// Just rebuild the whole list for simplicity
	// In production, could optimize to update single member
	ml.rebuildList()
}

func (ml *membersList) rebuildList() {
	if !ml.currentGuildID.IsValid() {
		ml.Clear()
		return
	}

	if !ml.visible {
		return // Don't rebuild if not visible
	}

	// Fetch all members
	members, err := discordState.Cabinet.Members(ml.currentGuildID)
	if err != nil {
		slog.Error("failed to get members", "guild_id", ml.currentGuildID, "err", err)
		ml.Clear()
		ml.AddItem("Failed to load members", "", 0, nil)
		return
	}

	// Build member items
	var memberItems []*memberItem
	for i := range members {
		memberItems = append(memberItems, &memberItem{
			Member:   &members[i],
			Presence: ml.getPresence(ml.currentGuildID, members[i].User.ID),
			RoleInfo: ml.getRoleInfo(ml.currentGuildID, &members[i]),
		})
	}

	// Sort by status (online first), then by name
	slices.SortFunc(memberItems, ml.sortMembers)

	// Group by role
	grouped := ml.groupMembersByRole(memberItems)

	// Clear and rebuild the list
	ml.Clear()
	ml.memberItems = make(map[discord.UserID]int)

	itemIndex := 0

	// Get all role names and sort them
	roleNames := make([]string, 0, len(grouped))
	for roleName := range grouped {
		roleNames = append(roleNames, roleName)
	}
	slices.Sort(roleNames)

	// Add role sections
	for _, roleName := range roleNames {
		roleMembers := grouped[roleName]

		// Add role header
		ml.AddItem(fmt.Sprintf("─ %s ─", roleName), "", 0, nil)
		itemIndex++

		// Add members in this role
		for _, member := range roleMembers {
			ml.AddItem(ml.formatMemberText(member), "", 0, nil)
			ml.memberItems[member.Member.User.ID] = itemIndex
			itemIndex++
		}
	}

	// Only draw if we have focus or the members list is visible
	// Don't trigger a full app redraw that could affect other components
}

func (ml *membersList) sortMembers(a, b *memberItem) int {
	statusOrder := map[discord.Status]int{
		discord.OnlineStatus:       0,
		discord.IdleStatus:         1,
		discord.DoNotDisturbStatus: 2,
		discord.OfflineStatus:      3,
		discord.InvisibleStatus:    3,
	}

	aStatus := discord.OfflineStatus
	if a.Presence != nil {
		aStatus = a.Presence.Status
	}
	bStatus := discord.OfflineStatus
	if b.Presence != nil {
		bStatus = b.Presence.Status
	}

	if statusOrder[aStatus] != statusOrder[bStatus] {
		return cmp.Compare(statusOrder[aStatus], statusOrder[bStatus])
	}

	aName := a.Member.Nick
	if aName == "" {
		aName = a.Member.User.DisplayOrUsername()
	}
	bName := b.Member.Nick
	if bName == "" {
		bName = b.Member.User.DisplayOrUsername()
	}

	return strings.Compare(strings.ToLower(aName), strings.ToLower(bName))
}

func (ml *membersList) groupMembersByRole(members []*memberItem) map[string][]*memberItem {
	grouped := make(map[string][]*memberItem)

	for _, member := range members {
		roleName := "No Role"
		if member.RoleInfo != nil {
			roleName = member.RoleInfo.Name
		}

		grouped[roleName] = append(grouped[roleName], member)
	}

	return grouped
}

func (ml *membersList) getPresence(guildID discord.GuildID, userID discord.UserID) *discord.Presence {
	presence, err := discordState.Cabinet.Presence(guildID, userID)
	if err != nil {
		return &discord.Presence{
			User:   discord.User{ID: userID},
			Status: discord.OfflineStatus,
		}
	}
	return presence
}

func (ml *membersList) getRoleInfo(guildID discord.GuildID, member *discord.Member) *discord.Role {
	if len(member.RoleIDs) == 0 {
		return nil
	}

	var highestRole *discord.Role
	var highestPos int

	for _, roleID := range member.RoleIDs {
		role, err := discordState.Cabinet.Role(guildID, roleID)
		if err != nil {
			continue
		}

		if role.Color != 0 && role.Position > highestPos {
			highestRole = role
			highestPos = role.Position
		}
	}

	return highestRole
}

func (ml *membersList) getStatusIndicator(status discord.Status) string {
	switch status {
	case discord.OnlineStatus:
		return "[green::b]•[-:-:-]"
	case discord.IdleStatus:
		return "[yellow::b]•[-:-:-]"
	case discord.DoNotDisturbStatus:
		return "[red::b]•[-:-:-]"
	default:
		return "[::d]•[::D]" // Gray, dimmed
	}
}

func (ml *membersList) formatMemberText(item *memberItem) string {
	status := discord.OfflineStatus
	if item.Presence != nil {
		status = item.Presence.Status
	}

	name := item.Member.User.DisplayOrUsername()
	if item.Member.Nick != "" {
		name = item.Member.Nick
	}

	var text strings.Builder
	text.WriteString(ml.getStatusIndicator(status))
	text.WriteString(" ")

	if item.RoleInfo != nil && item.RoleInfo.Color != 0 {
		color := tcell.NewHexColor(int32(item.RoleInfo.Color))
		text.WriteString(fmt.Sprintf("[%s]%s[-]", color.String(), name))
	} else {
		text.WriteString(name)
	}

	// Dim offline members
	if status == discord.OfflineStatus || status == discord.InvisibleStatus {
		return fmt.Sprintf("[::d]%s[::D]", text.String())
	}

	return text.String()
}

func (ml *membersList) onSelected(index int) {
	if index < 0 || index >= ml.GetItemCount() {
		return
	}

	// Get the main text to check if it's a role header
	mainText, _ := ml.GetItemText(index)

	// Skip if this is a role header (contains "─")
	if strings.Contains(mainText, "─") {
		return
	}

	// Find the member from our cache
	var userID discord.UserID
	for id, idx := range ml.memberItems {
		if idx == index {
			userID = id
			break
		}
	}

	if !userID.IsValid() {
		return
	}

	// Initiate DM
	go func() {
		if err := initiateDM(userID); err != nil {
			slog.Error("failed to initiate DM", "user_id", userID, "err", err)
		}
	}()
}

func (ml *membersList) onInputCapture(event *tcell.EventKey) *tcell.EventKey {
	switch event.Name() {
	case ml.cfg.Keys.MembersList.SelectPrevious:
		return tcell.NewEventKey(tcell.KeyUp, "", tcell.ModNone)
	case ml.cfg.Keys.MembersList.SelectNext:
		return tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone)
	case ml.cfg.Keys.MembersList.SelectFirst:
		return tcell.NewEventKey(tcell.KeyHome, "", tcell.ModNone)
	case ml.cfg.Keys.MembersList.SelectLast:
		return tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModNone)
	case ml.cfg.Keys.MembersList.InitiateDM:
		ml.onSelected(ml.GetCurrentItem())
		return nil
	}

	return nil
}
