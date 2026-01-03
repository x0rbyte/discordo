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
	"github.com/diamondburned/arikawa/v3/utils/httputil"
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
		fl.SetTitle("Friends (Press 'a' to add)")
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
			fl.AddItem("─ Pending Incoming (Enter=Accept, d=Deny) ─", "", 0, nil)
			itemIndex++

			for _, rel := range pendingIncoming {
				fl.AddItem("[yellow]<- "+rel.User.DisplayOrUsername()+" (incoming)[-]", "", 0, nil)
				fl.friendItems[itemIndex] = rel.User.ID
				itemIndex++
			}
		}

		if len(pendingOutgoing) > 0 {
			fl.AddItem("─ Pending Outgoing (x=Cancel) ─", "", 0, nil)
			itemIndex++

			for _, rel := range pendingOutgoing {
				fl.AddItem("[::d]-> "+rel.User.DisplayOrUsername()+" (outgoing)[::D]", "", 0, nil)
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

func (fl *friendsList) getRelationshipType(userID discord.UserID) discord.RelationshipType {
	for _, rel := range fl.relationships {
		if rel.User.ID == userID {
			return rel.Type
		}
	}
	return 0
}

func (fl *friendsList) acceptFriendRequest(userID discord.UserID) {
	err := discordState.RequestJSON(
		nil,
		"PUT",
		api.EndpointMe+"/relationships/"+userID.String(),
	)
	if err != nil {
		slog.Error("failed to accept friend request", "user_id", userID, "err", err)
		return
	}

	slog.Info("accepted friend request", "user_id", userID)
	// Refresh the list
	go fl.show()
}

func (fl *friendsList) denyFriendRequest(userID discord.UserID) {
	err := discordState.RequestJSON(
		nil,
		"DELETE",
		api.EndpointMe+"/relationships/"+userID.String(),
	)
	if err != nil {
		slog.Error("failed to deny friend request", "user_id", userID, "err", err)
		return
	}

	slog.Info("denied friend request", "user_id", userID)
	// Refresh the list
	go fl.show()
}

func (fl *friendsList) cancelFriendRequest(userID discord.UserID) {
	err := discordState.RequestJSON(
		nil,
		"DELETE",
		api.EndpointMe+"/relationships/"+userID.String(),
	)
	if err != nil {
		slog.Error("failed to cancel friend request", "user_id", userID, "err", err)
		return
	}

	slog.Info("cancelled friend request", "user_id", userID)
	// Refresh the list
	go fl.show()
}

func (fl *friendsList) sendFriendRequest(username string) {
	// Parse username - Discord uses username or username#discriminator format
	var user, discriminator string
	parts := strings.Split(username, "#")
	if len(parts) == 2 {
		user = parts[0]
		discriminator = parts[1]
	} else {
		user = username
		discriminator = "0" // New username system doesn't use discriminators
	}

	type friendRequestPayload struct {
		Username      string `json:"username"`
		Discriminator string `json:"discriminator"`
	}

	payload := friendRequestPayload{
		Username:      user,
		Discriminator: discriminator,
	}

	err := discordState.RequestJSON(
		nil,
		"POST",
		api.EndpointMe+"/relationships",
		httputil.WithJSONBody(payload),
	)
	if err != nil {
		slog.Error("failed to send friend request", "username", username, "err", err)
		return
	}

	slog.Info("sent friend request", "username", username)
	// Refresh the list
	go fl.show()
}

func (fl *friendsList) showAddFriendDialog() {
	inputField := tview.NewInputField().
		SetLabel("Add Friend (username): ").
		SetFieldWidth(0)

	inputField.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			username := inputField.GetText()
			if username != "" {
				fl.sendFriendRequest(username)
			}
		}
		// Close dialog
		app.chatView.RemovePage("addFriendDialog").SwitchToPage(friendsListPageName)
		app.SetFocus(fl)
	})

	inputField.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			app.chatView.RemovePage("addFriendDialog").SwitchToPage(friendsListPageName)
			app.SetFocus(fl)
			return nil
		}
		return event
	})

	grid := tview.NewGrid().
		SetRows(0, 3, 0).
		SetColumns(0, 50, 0).
		AddItem(inputField, 1, 1, 1, 1, 0, 0, true)

	modal := tview.NewFrame(grid).
		SetBorders(1, 1, 1, 1, 1, 1).
		AddText("Add Friend", true, tview.AlignmentCenter, tcell.ColorDefault)

	app.chatView.AddPage("addFriendDialog", modal, true, true)
	app.SetFocus(inputField)
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

	// Check relationship type
	relType := fl.getRelationshipType(userID)

	// If it's a pending incoming request, accept it
	if relType == 3 { // Pending incoming
		fl.acceptFriendRequest(userID)
		return
	}

	// For friends or other types, initiate DM
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
		// Enter accepts incoming requests or initiates DM
		fl.onSelected(fl.GetCurrentItem())
		return nil
	case tcell.KeyEscape:
		// Esc clears search first, then closes modal
		fl.clearSearch()
		return nil
	case tcell.KeyRune:
		str := event.Str()
		if len(str) > 0 {
			char := rune(str[0])

			// Handle special action keys if not searching
			if fl.searchQuery == "" {
				index := fl.GetCurrentItem()
				userID, ok := fl.friendItems[index]
				if ok && userID.IsValid() {
					relType := fl.getRelationshipType(userID)

					switch char {
					case 'd', 'D':
						// Deny pending incoming friend request
						if relType == 3 { // Pending incoming
							fl.denyFriendRequest(userID)
							return nil
						}
					case 'x', 'X':
						// Cancel pending outgoing friend request
						if relType == 4 { // Pending outgoing
							fl.cancelFriendRequest(userID)
							return nil
						}
					case 'a', 'A':
						// Add friend request (show input dialog)
						fl.showAddFriendDialog()
						return nil
					}
				} else if char == 'a' || char == 'A' {
					// Allow 'a' to work even when not on a user
					fl.showAddFriendDialog()
					return nil
				}
			}

			// Add character to search
			fl.updateSearch(char)
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
