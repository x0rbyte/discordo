package cmd

import (
	"fmt"
	"log/slog"

	"github.com/ayn2op/discordo/internal/config"
	"github.com/ayn2op/discordo/internal/keyring"
	"github.com/ayn2op/discordo/internal/ui"
	"github.com/ayn2op/tview"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/gdamore/tcell/v3"
)

const (
	flexPageName            = "flex"
	mentionsListPageName    = "mentionsList"
	attachmentsListPageName = "attachmentsList"
	confirmModalPageName    = "confirmModal"
	friendsListPageName     = "friendsList"
	reactionPickerPageName  = "reactionPicker"
	joinServerPageName      = "joinServer"
	pinnedMessagesPageName  = "pinnedMessages"
)

type chatView struct {
	*tview.Pages

	mainFlex  *tview.Flex
	rightFlex *tview.Flex

	guildsTree   *guildsTree
	messagesList *messagesList
	messageInput *messageInput
	membersList  *membersList

	selectedChannel *discord.Channel

	app *tview.Application
	cfg *config.Config
}

func newChatView(app *tview.Application, cfg *config.Config) *chatView {
	chatView := &chatView{
		Pages: tview.NewPages(),

		mainFlex:  tview.NewFlex(),
		rightFlex: tview.NewFlex(),

		guildsTree:   newGuildsTree(cfg),
		messagesList: newMessagesList(cfg),
		messageInput: newMessageInput(cfg),
		membersList:  newMembersList(cfg),

		app: app,
		cfg: cfg,
	}

	chatView.SetInputCapture(chatView.onInputCapture)

	chatView.buildLayout()
	return chatView
}

func (cv *chatView) buildLayout() {
	cv.Clear()
	cv.rightFlex.Clear()
	cv.mainFlex.Clear()

	cv.rightFlex.
		SetDirection(tview.FlexRow).
		AddItem(cv.messagesList, 0, 1, false).
		AddItem(cv.messageInput, 3, 1, false)

	// Build layout based on membersList visibility
	if cv.membersList.visible {
		// 3-column layout: [guildsTree | rightFlex | membersList]
		cv.mainFlex.
			AddItem(cv.guildsTree, 0, 1, true).
			AddItem(cv.rightFlex, 0, 4, false).
			AddItem(cv.membersList, 0, 1, false)
	} else {
		// 2-column layout: [guildsTree | rightFlex]
		cv.mainFlex.
			AddItem(cv.guildsTree, 0, 1, true).
			AddItem(cv.rightFlex, 0, 4, false)
	}

	cv.AddAndSwitchToPage(flexPageName, cv.mainFlex, true)
}

func (cv *chatView) toggleGuildsTree() {
	// The guilds tree is visible if the number of items is two or three
	if cv.mainFlex.GetItemCount() >= 2 {
		cv.mainFlex.RemoveItem(cv.guildsTree)
		if cv.guildsTree.HasFocus() {
			cv.app.SetFocus(cv.mainFlex)
		}
	} else {
		cv.buildLayout()
		cv.app.SetFocus(cv.guildsTree)
	}
}

func (cv *chatView) toggleMembersList() {
	cv.membersList.visible = !cv.membersList.visible

	if cv.membersList.visible {
		// Add members list as 3rd column
		cv.mainFlex.AddItem(cv.membersList, 0, 1, false)
		// Update the members list if we have a current guild
		// Use updateForGuild instead of rebuildList to ensure members are requested if not cached
		if cv.membersList.currentGuildID.IsValid() {
			cv.membersList.updateForGuild(cv.membersList.currentGuildID)
		}
	} else {
		// Remove members list
		cv.mainFlex.RemoveItem(cv.membersList)
		if cv.membersList.HasFocus() {
			cv.app.SetFocus(cv.messagesList)
		}
	}
}

func (cv *chatView) focusGuildsTree() bool {
	// The guilds tree is not hidden if the number of items is two or three
	if cv.mainFlex.GetItemCount() >= 2 {
		cv.app.SetFocus(cv.guildsTree)
		return true
	}

	return false
}

func (cv *chatView) focusMembersList() bool {
	if cv.membersList.visible {
		cv.app.SetFocus(cv.membersList)
		return true
	}
	return false
}

func (cv *chatView) focusMessageInput() bool {
	if !cv.messageInput.GetDisabled() {
		cv.app.SetFocus(cv.messageInput)
		return true
	}

	return false
}

func (cv *chatView) focusPrevious() {
	switch cv.app.GetFocus() {
	case cv.guildsTree:
		cv.focusMessageInput()
	case cv.messagesList:
		if ok := cv.focusGuildsTree(); !ok {
			cv.app.SetFocus(cv.messageInput)
		}
	case cv.membersList:
		cv.app.SetFocus(cv.messagesList)
	case cv.messageInput:
		if cv.membersList.visible {
			cv.app.SetFocus(cv.membersList)
		} else {
			cv.app.SetFocus(cv.messagesList)
		}
	}
}

func (cv *chatView) focusNext() {
	switch cv.app.GetFocus() {
	case cv.guildsTree:
		cv.app.SetFocus(cv.messagesList)
	case cv.messagesList:
		if cv.membersList.visible {
			cv.app.SetFocus(cv.membersList)
		} else {
			cv.focusMessageInput()
		}
	case cv.membersList:
		cv.focusMessageInput()
	case cv.messageInput:
		if ok := cv.focusGuildsTree(); !ok {
			cv.app.SetFocus(cv.messagesList)
		}
	}
}

func (cv *chatView) onInputCapture(event *tcell.EventKey) *tcell.EventKey {
	switch event.Name() {
	case cv.cfg.Keys.FocusGuildsTree:
		cv.messageInput.removeMentionsList()
		cv.focusGuildsTree()
		return nil
	case cv.cfg.Keys.FocusMessagesList:
		cv.messageInput.removeMentionsList()
		cv.app.SetFocus(cv.messagesList)
		return nil
	case cv.cfg.Keys.FocusMessageInput:
		cv.focusMessageInput()
		return nil
	case cv.cfg.Keys.FocusMembersList:
		cv.focusMembersList()
		return nil
	case cv.cfg.Keys.FocusPrevious:
		cv.focusPrevious()
		return nil
	case cv.cfg.Keys.FocusNext:
		cv.focusNext()
		return nil
	case cv.cfg.Keys.Logout:
		app.quit()

		if err := keyring.DeleteToken(); err != nil {
			slog.Error("failed to delete token from keyring", "err", err)
			return nil
		}

		return nil
	case cv.cfg.Keys.ToggleGuildsTree:
		cv.toggleGuildsTree()
		return nil
	case cv.cfg.Keys.ToggleMembersList:
		cv.toggleMembersList()
		return nil
	case cv.cfg.Keys.ShowFriendsList:
		cv.showFriendsList()
		return nil
	case cv.cfg.Keys.CloseCurrentDM:
		cv.closeCurrentDM()
		return nil
	case cv.cfg.Keys.ToggleMute:
		cv.toggleMuteCurrentChannel()
		return nil
	case cv.cfg.Keys.JoinServer:
		cv.showJoinServer()
		return nil
	case cv.cfg.Keys.ShowPinnedMessages:
		cv.showPinnedMessages()
		return nil
	}

	return event
}

func (cv *chatView) showConfirmModal(prompt string, buttons []string, onDone func(label string)) {
	previousFocus := cv.app.GetFocus()

	modal := tview.NewModal().
		SetText(prompt).
		AddButtons(buttons).
		SetDoneFunc(func(_ int, buttonLabel string) {
			cv.RemovePage(confirmModalPageName).SwitchToPage(flexPageName)
			cv.app.SetFocus(previousFocus)

			if onDone != nil {
				onDone(buttonLabel)
			}
		})

	cv.
		AddAndSwitchToPage(confirmModalPageName, ui.Centered(modal, 0, 0), true).
		ShowPage(flexPageName)
}

func (cv *chatView) showFriendsList() {
	previousFocus := cv.app.GetFocus()

	fl := newFriendsList(cv.cfg)
	fl.SetDoneFunc(func() {
		cv.RemovePage(friendsListPageName).SwitchToPage(flexPageName)
		cv.app.SetFocus(previousFocus)
	})

	cv.AddAndSwitchToPage(friendsListPageName, ui.Centered(fl, 50, 20), true).
		ShowPage(flexPageName)

	go fl.show()
}

func (cv *chatView) showJoinServer() {
	previousFocus := cv.app.GetFocus()

	var inviteCode string
	form := tview.NewForm()
	form.AddInputField("Invite Code:", "", 30, func(text string) {
		inviteCode = text
	})
	form.AddButton("Join", func() {
		if inviteCode == "" {
			return
		}
		cv.RemovePage(joinServerPageName).SwitchToPage(flexPageName)
		cv.app.SetFocus(previousFocus)
		go cv.joinServer(inviteCode)
	})
	form.AddButton("Cancel", func() {
		cv.RemovePage(joinServerPageName).SwitchToPage(flexPageName)
		cv.app.SetFocus(previousFocus)
	})

	form.Box = ui.ConfigureBox(form.Box, &cv.cfg.Theme)
	form.SetTitle("Join Server")

	cv.AddAndSwitchToPage(joinServerPageName, ui.Centered(form, 50, 10), true).
		ShowPage(flexPageName)
}

func (cv *chatView) joinServer(inviteCode string) {
	slog.Info("joining server", "invite_code", inviteCode)

	result, err := discordState.JoinInvite(inviteCode)
	if err != nil {
		slog.Error("failed to join server", "err", err, "invite_code", inviteCode)
		return
	}

	slog.Info("successfully joined server", "guild_id", result.Guild.ID, "guild_name", result.Guild.Name)

	// Add the guild to the tree on UI thread
	cv.app.QueueUpdateDraw(func() {
		root := cv.guildsTree.GetRoot()
		cv.guildsTree.createGuildNode(root, result.Guild)
	})
}

func (cv *chatView) showPinnedMessages() {
	if cv.selectedChannel == nil {
		slog.Debug("showPinnedMessages: no channel selected")
		return
	}

	previousFocus := cv.app.GetFocus()
	channelID := cv.selectedChannel.ID

	list := tview.NewList().
		SetWrapAround(true).
		SetHighlightFullLine(true).
		ShowSecondaryText(true)

	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Name() {
		case cv.cfg.Keys.MessagesList.SelectPrevious:
			return tcell.NewEventKey(tcell.KeyUp, "", tcell.ModNone)
		case cv.cfg.Keys.MessagesList.SelectNext:
			return tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone)
		case cv.cfg.Keys.MessagesList.SelectFirst:
			return tcell.NewEventKey(tcell.KeyHome, "", tcell.ModNone)
		case cv.cfg.Keys.MessagesList.SelectLast:
			return tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModNone)
		case "Esc", cv.cfg.Keys.MessagesList.Cancel:
			cv.RemovePage(pinnedMessagesPageName).SwitchToPage(flexPageName)
			cv.app.SetFocus(previousFocus)
			return nil
		}
		return event
	})

	list.Box = ui.ConfigureBox(list.Box, &cv.cfg.Theme)
	list.SetTitle("Pinned Messages")

	cv.AddAndSwitchToPage(pinnedMessagesPageName, ui.Centered(list, 80, 20), true).
		ShowPage(flexPageName)

	// Fetch pinned messages in background
	go func() {
		messages, err := discordState.PinnedMessages(channelID)
		if err != nil {
			slog.Error("failed to get pinned messages", "err", err, "channel_id", channelID)
			cv.app.QueueUpdateDraw(func() {
				list.AddItem("Failed to load pinned messages", "", 0, nil)
			})
			return
		}

		if len(messages) == 0 {
			cv.app.QueueUpdateDraw(func() {
				list.AddItem("No pinned messages", "", 0, nil)
			})
			return
		}

		cv.app.QueueUpdateDraw(func() {
			for i, msg := range messages {
				// Format: "Author: Content preview"
				author := msg.Author.DisplayOrUsername()
				content := msg.Content
				if len(content) > 60 {
					content = content[:57] + "..."
				}
				if content == "" {
					content = "[attachment or embed]"
				}

				mainText := fmt.Sprintf("%s: %s", author, content)
				timestamp := msg.Timestamp.Time().Format("Jan 02 3:04PM")

				// Capture the full message for the detail view
				fullMsg := msg

				list.AddItem(mainText, timestamp, rune('1'+i), func() {
					// Show detail view with options
					cv.RemovePage(pinnedMessagesPageName).SwitchToPage(flexPageName)
					cv.showPinnedMessageDetail(fullMsg, previousFocus)
				})
			}
		})
	}()
}

func (cv *chatView) showPinnedMessageDetail(msg discord.Message, previousFocus tview.Primitive) {
	// Create a text view to show the full message
	textView := tview.NewTextView().
		SetDynamicColors(true).
		SetWordWrap(true).
		SetScrollable(true)

	// Format the full message
	author := msg.Author.DisplayOrUsername()
	timestamp := msg.Timestamp.Time().Format("Jan 02, 2006 at 3:04PM")
	content := msg.Content
	if content == "" {
		content = "[No text content]"
	}

	fmt.Fprintf(textView, "[::b]%s[::B]\n", author)
	fmt.Fprintf(textView, "[::d]%s[::D]\n\n", timestamp)
	fmt.Fprintf(textView, "%s\n\n", content)

	if len(msg.Attachments) > 0 {
		fmt.Fprintf(textView, "[::d]Attachments:[::D]\n")
		for _, att := range msg.Attachments {
			fmt.Fprintf(textView, "  • %s\n", att.Filename)
		}
		fmt.Fprintln(textView)
	}

	textView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Name() {
		case "Esc":
			cv.RemovePage(pinnedMessagesPageName).SwitchToPage(flexPageName)
			cv.app.SetFocus(previousFocus)
			return nil
		case "Rune[u]", "Rune[U]":
			// Unpin this message
			cv.RemovePage(pinnedMessagesPageName).SwitchToPage(flexPageName)
			cv.app.SetFocus(previousFocus)
			go cv.unpinMessageByID(msg.ChannelID, msg.ID)
			return nil
		}
		return event
	})

	textView.Box = ui.ConfigureBox(textView.Box, &cv.cfg.Theme)
	textView.SetTitle("Pinned Message (Press U to unpin, Esc to close)")

	cv.AddAndSwitchToPage(pinnedMessagesPageName, ui.Centered(textView, 80, 20), true).
		ShowPage(flexPageName)
}

func (cv *chatView) unpinMessageByID(channelID discord.ChannelID, messageID discord.MessageID) {
	slog.Info("unpinning message", "channel_id", channelID, "message_id", messageID)

	if err := discordState.UnpinMessage(channelID, messageID, ""); err != nil {
		slog.Error("failed to unpin message", "channel_id", channelID, "message_id", messageID, "err", err)
		return
	}

	slog.Info("successfully unpinned message", "message_id", messageID)
}

func (cv *chatView) closeCurrentDM() {
	// This is called from onInputCapture which runs on UI thread
	// Check if we have a selected channel
	if cv.selectedChannel == nil {
		slog.Debug("closeCurrentDM: no channel selected")
		return
	}

	// Check if it's a DM channel
	if cv.selectedChannel.Type != discord.DirectMessage && cv.selectedChannel.Type != discord.GroupDM {
		slog.Debug("closeCurrentDM: current channel is not a DM", "type", cv.selectedChannel.Type)
		return
	}

	channelID := cv.selectedChannel.ID
	slog.Info("closing current DM channel", "channel_id", channelID)

	// First find the Direct Messages node
	var dmNode *tview.TreeNode
	cv.guildsTree.GetRoot().Walk(func(node, parent *tview.TreeNode) bool {
		// Check for "Direct Messages" text, not just nil reference (folders also have nil ref)
		if node.GetText() == "Direct Messages" && parent == cv.guildsTree.GetRoot() {
			dmNode = node
			return false
		}
		return true
	})

	if dmNode == nil {
		slog.Error("Direct Messages node not found in tree")
		return
	}

	// Then find the channel node within the DM node
	var channelNode *tview.TreeNode
	var nodesChecked int
	dmNode.Walk(func(node, parent *tview.TreeNode) bool {
		nodesChecked++
		nodeRef := node.GetReference()
		slog.Debug("checking node in DM walk", "text", node.GetText(), "ref_type", fmt.Sprintf("%T", nodeRef), "looking_for", channelID)

		if ref, ok := nodeRef.(discord.ChannelID); ok && ref == channelID {
			channelNode = node
			slog.Info("found matching channel node", "channel_id", channelID)
			return false
		}
		return true
	})

	slog.Info("walk completed", "nodes_checked", nodesChecked, "dm_children", len(dmNode.GetChildren()))

	if channelNode == nil {
		slog.Error("channel node not found in DM list", "channel_id", channelID, "dm_node_children", len(dmNode.GetChildren()))
		return
	}

	// Remove the channel from the tree
	slog.Info("removing DM from tree", "channel_id", channelID)
	dmNode.RemoveChild(channelNode)

	// Clear the selection
	cv.selectedChannel = nil
	cv.messagesList.reset()
	cv.messageInput.SetDisabled(true)
	cv.messageInput.SetPlaceholder("Select a channel to start chatting")

	// Close the DM on Discord's side (removes from DM list on all clients)
	go func() {
		slog.Info("deleting DM channel on Discord", "channel_id", channelID)
		err := discordState.DeleteChannel(channelID, "")
		if err != nil {
			slog.Error("failed to delete DM channel on Discord", "channel_id", channelID, "err", err)
		} else {
			slog.Info("DM channel deleted on Discord", "channel_id", channelID)
		}
	}()
}

func (cv *chatView) toggleMuteCurrentChannel() {
	// Get the currently selected node from the guilds tree
	node := cv.guildsTree.GetCurrentNode()
	if node == nil {
		slog.Debug("toggleMuteCurrentChannel: no node selected")
		return
	}

	ref := node.GetReference()

	// Check if it's a guild or a channel
	if guildID, ok := ref.(discord.GuildID); ok && guildID.IsValid() {
		slog.Info("toggling guild mute via Ctrl+M", "guild_id", guildID)
		go cv.guildsTree.toggleGuildMute(guildID)
	} else if channelID, ok := ref.(discord.ChannelID); ok && channelID.IsValid() {
		slog.Info("toggling channel mute via Ctrl+M", "channel_id", channelID)
		go cv.guildsTree.toggleChannelMute(channelID)
	} else {
		slog.Debug("toggleMuteCurrentChannel: selected node is not a guild or channel")
	}
}

func (cv *chatView) leaveCurrentGuild() {
	// Get the currently selected node from the guilds tree
	node := cv.guildsTree.GetCurrentNode()
	if node == nil {
		slog.Debug("leaveCurrentGuild: no node selected")
		return
	}

	ref := node.GetReference()

	// Check if it's a guild
	guildID, ok := ref.(discord.GuildID)
	if !ok || !guildID.IsValid() {
		slog.Debug("leaveCurrentGuild: selected node is not a guild")
		return
	}

	// Get guild name for confirmation
	guild, err := discordState.Cabinet.Guild(guildID)
	if err != nil {
		slog.Error("failed to get guild", "guild_id", guildID, "err", err)
		return
	}

	// Show confirmation modal
	cv.showConfirmModal(
		fmt.Sprintf("Are you sure you want to leave '%s'?", guild.Name),
		[]string{"Yes", "No"},
		func(label string) {
			if label == "Yes" {
				go cv.leaveGuild(guildID, node)
			}
		},
	)
}

func (cv *chatView) leaveGuild(guildID discord.GuildID, node *tview.TreeNode) {
	slog.Info("leaving guild", "guild_id", guildID)

	err := discordState.LeaveGuild(guildID)
	if err != nil {
		slog.Error("failed to leave guild", "err", err, "guild_id", guildID)
		return
	}

	slog.Info("successfully left guild", "guild_id", guildID)

	// Remove the guild from the tree on UI thread
	cv.app.QueueUpdateDraw(func() {
		root := cv.guildsTree.GetRoot()
		root.RemoveChild(node)
	})
}
