package cmd

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/ayn2op/discordo/internal/config"
	"github.com/ayn2op/discordo/internal/ui"
	"github.com/ayn2op/tview"
	"github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/gdamore/tcell/v3"
)

type friendsList struct {
	*tview.List
	cfg *config.Config

	relationships []discord.Relationship
	friendItems   map[int]discord.UserID // list index -> UserID
	searchQuery   string
}

func newFriendsList(cfg *config.Config) *friendsList {
	fl := &friendsList{
		List:        tview.NewList(),
		cfg:         cfg,
		friendItems: make(map[int]discord.UserID),
	}

	fl.Box = ui.ConfigureBox(fl.Box, &cfg.Theme)
	fl.SetTitle("Friends")
	fl.SetInputCapture(fl.onInputCapture)
	fl.SetSelectedFunc(func(index int, _, _ string, _ rune) {
		fl.onSelected(index)
	})
	fl.ShowSecondaryText(false)
	fl.SetHighlightFullLine(true)

	return fl
}

func (fl *friendsList) show() {
	slog.Debug("friends list show() called")

	// This is called from a goroutine in chatview, so we need to use QueueUpdateDraw
	// for ALL UI operations

	// Show loading message on main thread
	app.QueueUpdateDraw(func() {
		fl.Clear()
		fl.AddItem("Loading friends...", "", 0, nil)
	})

	// Fetch relationships (blocking network call - safe because we're already in a goroutine)
	err := fl.fetchRelationships()
	if err != nil {
		slog.Error("failed to fetch relationships", "err", err)

		// Show error in the list
		app.QueueUpdateDraw(func() {
			fl.Clear()
			fl.AddItem("Failed to load friends list", "", 0, nil)
			fl.AddItem("Error: "+err.Error(), "", 0, nil)
		})
		return
	}

	slog.Debug("friends relationships fetched successfully", "count", len(fl.relationships))

	// Update UI with friends list
	app.QueueUpdateDraw(func() {
		fl.rebuildList()
	})
}

func (fl *friendsList) hide() {
	app.chatView.RemovePage(friendsListPageName).SwitchToPage(flexPageName)
}

func (fl *friendsList) fetchRelationships() error {
	var relationships []discord.Relationship

	// Use raw API endpoint (not directly exposed in arikawa)
	err := discordState.RequestJSON(
		&relationships,
		"GET",
		api.EndpointMe+"/relationships",
	)
	if err != nil {
		return fmt.Errorf("failed to fetch relationships: %w", err)
	}

	fl.relationships = relationships
	return nil
}

func (fl *friendsList) rebuildList() {
	slog.Debug("rebuildList() called", "total_relationships", len(fl.relationships))

	fl.Clear()
	fl.friendItems = make(map[int]discord.UserID)

	// Update title to show search query
	if fl.searchQuery != "" {
		fl.SetTitle(fmt.Sprintf("Friends (search: %s)", fl.searchQuery))
	} else {
		fl.SetTitle("Friends")
	}

	if len(fl.relationships) == 0 {
		fl.AddItem("No friends found", "", 0, nil)
		return
	}

	slog.Debug("filtering friends")

	// Pre-cache all presences to avoid expensive lookups later
	presenceCache := make(map[discord.UserID]*discord.Presence)
	guilds, _ := discordState.Cabinet.Guilds()
	for _, guild := range guilds {
		presences, _ := discordState.Cabinet.Presences(guild.ID)
		for _, presence := range presences {
			// Keep the first presence we find for each user
			if _, exists := presenceCache[presence.User.ID]; !exists {
				presenceCache[presence.User.ID] = &presence
			}
		}
	}
	slog.Debug("cached presences", "count", len(presenceCache))

	// Filter and sort friends
	var friends []discord.Relationship
	for _, rel := range fl.relationships {
		if rel.Type == discord.FriendRelationship {
			// Apply search filter
			if fl.searchQuery != "" {
				username := strings.ToLower(rel.User.DisplayOrUsername())
				query := strings.ToLower(fl.searchQuery)
				if !strings.Contains(username, query) {
					continue
				}
			}
			friends = append(friends, rel)
		}
	}

	slog.Debug("friends filtered", "count", len(friends))

	// Sort by username
	slog.Debug("sorting friends")
	slices.SortFunc(friends, func(a, b discord.Relationship) int {
		return strings.Compare(
			strings.ToLower(a.User.DisplayOrUsername()),
			strings.ToLower(b.User.DisplayOrUsername()),
		)
	})
	slog.Debug("friends sorted")

	// Group friends by online/offline status using cached presences
	var onlineFriends, offlineFriends []discord.Relationship
	for _, friend := range friends {
		presence := presenceCache[friend.User.ID]
		if presence != nil && presence.Status != discord.OfflineStatus && presence.Status != discord.InvisibleStatus {
			onlineFriends = append(onlineFriends, friend)
		} else {
			offlineFriends = append(offlineFriends, friend)
		}
	}
	slog.Debug("grouped friends", "online", len(onlineFriends), "offline", len(offlineFriends))

	itemIndex := 0

	// Add online friends section
	if len(onlineFriends) > 0 {
		slog.Debug("adding online friends")
		fl.AddItem(fmt.Sprintf("─ Online (%d) ─", len(onlineFriends)), "", 0, nil)
		itemIndex++

		for _, friend := range onlineFriends {
			presence := presenceCache[friend.User.ID]
			fl.AddItem(fl.formatFriendText(friend, presence), "", 0, nil)
			fl.friendItems[itemIndex] = friend.User.ID
			itemIndex++
		}
	}

	// Add offline friends section
	if len(offlineFriends) > 0 {
		slog.Debug("adding offline friends")
		fl.AddItem(fmt.Sprintf("─ Offline (%d) ─", len(offlineFriends)), "", 0, nil)
		itemIndex++

		for _, friend := range offlineFriends {
			fl.AddItem(fl.formatFriendText(friend, nil), "", 0, nil)
			fl.friendItems[itemIndex] = friend.User.ID
			itemIndex++
		}
	}

	// Only show pending/blocked if no search query
	if fl.searchQuery == "" {
		// Add pending/blocked sections if needed
		var pendingIncoming, pendingOutgoing, blocked []discord.Relationship
		for _, rel := range fl.relationships {
			switch rel.Type {
			case 3: // Pending incoming
				pendingIncoming = append(pendingIncoming, rel)
			case 4: // Pending outgoing
				pendingOutgoing = append(pendingOutgoing, rel)
			case 2: // Blocked
				blocked = append(blocked, rel)
			}
		}

		if len(pendingIncoming) > 0 {
			fl.AddItem("─ Pending Incoming ─", "", 0, nil)
			itemIndex++

			for _, rel := range pendingIncoming {
				fl.AddItem("[yellow]"+rel.User.DisplayOrUsername()+"[-]", "", 0, nil)
				fl.friendItems[itemIndex] = rel.User.ID
				itemIndex++
			}
		}

		if len(pendingOutgoing) > 0 {
			fl.AddItem("─ Pending Outgoing ─", "", 0, nil)
			itemIndex++

			for _, rel := range pendingOutgoing {
				fl.AddItem("[::d]"+rel.User.DisplayOrUsername()+"[::D]", "", 0, nil)
				fl.friendItems[itemIndex] = rel.User.ID
				itemIndex++
			}
		}
	}

	// Show message if search has no results
	if fl.searchQuery != "" && len(friends) == 0 {
		fl.AddItem(fmt.Sprintf("No friends matching '%s'", fl.searchQuery), "", 0, nil)
	}

	slog.Debug("rebuildList() complete")
	// Don't call app.Draw() here - we're already inside QueueUpdateDraw()
}

func (fl *friendsList) updateSearch(char rune) {
	if char == 0 {
		// Backspace - remove last character
		if len(fl.searchQuery) > 0 {
			fl.searchQuery = fl.searchQuery[:len(fl.searchQuery)-1]
		}
	} else {
		// Add character to search
		fl.searchQuery += string(char)
	}
	fl.rebuildList()
}

func (fl *friendsList) clearSearch() {
	if fl.searchQuery != "" {
		fl.searchQuery = ""
		fl.rebuildList()
	} else {
		fl.hide()
	}
}


func (fl *friendsList) getPresenceForUser(userID discord.UserID) *discord.Presence {
	// Try to find presence in any guild where we share membership
	// This is a best-effort approach since we don't track DM presences
	guilds, _ := discordState.Cabinet.Guilds()
	for _, guild := range guilds {
		presence, err := discordState.Cabinet.Presence(guild.ID, userID)
		if err == nil && presence != nil {
			return presence
		}
	}

	return nil
}

func (fl *friendsList) getStatusIndicator(userID discord.UserID) string {
	presence := fl.getPresenceForUser(userID)
	if presence == nil {
		return "[::d]•[::D]" // Gray, offline
	}

	switch presence.Status {
	case discord.OnlineStatus:
		return "[green::b]•[-:-:-]"
	case discord.IdleStatus:
		return "[yellow::b]•[-:-:-]"
	case discord.DoNotDisturbStatus:
		return "[red::b]•[-:-:-]"
	default:
		return "[::d]•[::D]" // Gray, offline
	}
}

func (fl *friendsList) formatFriendText(rel discord.Relationship, presence *discord.Presence) string {
	var text strings.Builder

	// Status indicator
	if presence != nil {
		switch presence.Status {
		case discord.OnlineStatus:
			text.WriteString("[green::b]•[-:-:-] ")
		case discord.IdleStatus:
			text.WriteString("[yellow::b]•[-:-:-] ")
		case discord.DoNotDisturbStatus:
			text.WriteString("[red::b]•[-:-:-] ")
		default:
			text.WriteString("[::d]•[::D] ") // Gray, offline
		}
	} else {
		text.WriteString("[::d]•[::D] ") // Gray, offline
	}

	// Username
	text.WriteString(rel.User.DisplayOrUsername())

	return text.String()
}

func (fl *friendsList) onSelected(index int) {
	if index < 0 || index >= fl.GetItemCount() {
		return
	}

	// Get the main text to check if it's a header
	mainText, _ := fl.GetItemText(index)

	// Skip if this is a header (contains "─")
	if strings.Contains(mainText, "─") {
		return
	}

	// Find the user ID from our cache
	userID, ok := fl.friendItems[index]
	if !ok || !userID.IsValid() {
		return
	}

	// Hide the modal
	fl.hide()

	// Initiate DM
	go func() {
		if err := initiateDM(userID); err != nil {
			slog.Error("failed to initiate DM", "user_id", userID, "err", err)
		}
	}()
}

func (fl *friendsList) onInputCapture(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyHome, tcell.KeyEnd, tcell.KeyPgUp, tcell.KeyPgDn:
		// Allow arrow keys and navigation keys through
		return event
	case tcell.KeyEnter:
		// Enter initiates DM
		fl.onSelected(fl.GetCurrentItem())
		return nil
	case tcell.KeyEscape:
		// Esc clears search first, then closes modal
		fl.clearSearch()
		return nil
	case tcell.KeyRune:
		// Add character to search
		str := event.Str()
		if len(str) > 0 {
			fl.updateSearch(rune(str[0]))
		}
		return nil
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		// Remove last character from search
		fl.updateSearch(0)
		return nil
	}

	// Check config-based keybindings
	switch event.Name() {
	case fl.cfg.Keys.FriendsList.SelectPrevious:
		return tcell.NewEventKey(tcell.KeyUp, "", tcell.ModNone)
	case fl.cfg.Keys.FriendsList.SelectNext:
		return tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone)
	case fl.cfg.Keys.FriendsList.SelectFirst:
		return tcell.NewEventKey(tcell.KeyHome, "", tcell.ModNone)
	case fl.cfg.Keys.FriendsList.SelectLast:
		return tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModNone)
	case fl.cfg.Keys.FriendsList.InitiateDM:
		fl.onSelected(fl.GetCurrentItem())
		return nil
	case fl.cfg.Keys.FriendsList.Cancel:
		fl.clearSearch()
		return nil
	}

	return nil
}
