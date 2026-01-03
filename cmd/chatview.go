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
