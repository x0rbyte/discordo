package cmd

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/ayn2op/discordo/internal/clipboard"
	"github.com/ayn2op/discordo/internal/config"
	"github.com/ayn2op/discordo/internal/ui"
	"github.com/ayn2op/tview"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/ningen/v3"
	"github.com/gdamore/tcell/v3"
)

type guildsTree struct {
	*tview.TreeView
	cfg *config.Config
}

func newGuildsTree(cfg *config.Config) *guildsTree {
	gt := &guildsTree{
		TreeView: tview.NewTreeView(),
		cfg:      cfg,
	}

	gt.Box = ui.ConfigureBox(gt.Box, &cfg.Theme)
	gt.
		SetRoot(tview.NewTreeNode("")).
		SetTopLevel(1).
		SetGraphics(cfg.Theme.GuildsTree.Graphics).
		SetGraphicsColor(tcell.GetColor(cfg.Theme.GuildsTree.GraphicsColor)).
		SetSelectedFunc(gt.onSelected).
		SetTitle("Guilds").
		SetInputCapture(gt.onInputCapture)

	return gt
}

func (gt *guildsTree) createFolderNode(folder gateway.GuildFolder) {
	name := "Folder"
	if folder.Name != "" {
		name = fmt.Sprintf("[%s]%s[-]", folder.Color, folder.Name)
	}

	folderNode := tview.NewTreeNode(name).SetExpanded(gt.cfg.Theme.GuildsTree.AutoExpandFolders)
	gt.GetRoot().AddChild(folderNode)

	for _, gID := range folder.GuildIDs {
		guild, err := discordState.Cabinet.Guild(gID)
		if err != nil {
			slog.Error("failed to get guild from state", "guild_id", gID, "err", err)
			continue
		}

		gt.createGuildNode(folderNode, *guild)
	}
}

func (gt *guildsTree) unreadStyle(indication ningen.UnreadIndication) tcell.Style {
	var style tcell.Style
	switch indication {
	case ningen.ChannelRead:
		style = style.Dim(true)
	case ningen.ChannelMentioned:
		style = style.Underline(true)
		fallthrough
	case ningen.ChannelUnread:
		style = style.Bold(true)
	}

	return style
}

func (gt *guildsTree) getGuildNodeStyle(guildID discord.GuildID) tcell.Style {
	indication := discordState.GuildIsUnread(guildID, ningen.GuildUnreadOpts{UnreadOpts: ningen.UnreadOpts{IncludeMutedCategories: true}})
	return gt.unreadStyle(indication)
}

func (gt *guildsTree) getChannelNodeStyle(channelID discord.ChannelID) tcell.Style {
	indication := discordState.ChannelIsUnread(channelID, ningen.UnreadOpts{IncludeMutedCategories: true})
	return gt.unreadStyle(indication)
}

func (gt *guildsTree) createGuildNode(n *tview.TreeNode, guild discord.Guild) {
	guildNode := tview.NewTreeNode(guild.Name).
		SetReference(guild.ID).
		SetTextStyle(gt.getGuildNodeStyle(guild.ID))
	n.AddChild(guildNode)
}

func (gt *guildsTree) createChannelNode(node *tview.TreeNode, channel discord.Channel) {
	if channel.Type != discord.DirectMessage && channel.Type != discord.GroupDM && !discordState.HasPermissions(channel.ID, discord.PermissionViewChannel) {
		return
	}

	channelNode := tview.NewTreeNode(ui.ChannelToString(channel)).
		SetReference(channel.ID).
		SetTextStyle(gt.getChannelNodeStyle(channel.ID))
	node.AddChild(channelNode)
}

func (gt *guildsTree) createChannelNodes(node *tview.TreeNode, channels []discord.Channel) {
	for _, channel := range channels {
		if channel.Type != discord.GuildCategory && !channel.ParentID.IsValid() {
			gt.createChannelNode(node, channel)
		}
	}

PARENT_CHANNELS:
	for _, channel := range channels {
		if channel.Type == discord.GuildCategory {
			for _, nested := range channels {
				if nested.ParentID == channel.ID {
					gt.createChannelNode(node, channel)
					continue PARENT_CHANNELS
				}
			}
		}
	}

	for _, channel := range channels {
		if channel.ParentID.IsValid() {
			var parent *tview.TreeNode
			node.Walk(func(node, _ *tview.TreeNode) bool {
				if node.GetReference() == channel.ParentID {
					parent = node
					return false
				}

				return true
			})

			if parent != nil {
				gt.createChannelNode(parent, channel)
			}
		}
	}
}

func (gt *guildsTree) onSelected(node *tview.TreeNode) {
	children := node.GetChildren()
	slog.Debug("onSelected called", "text", node.GetText(), "children", len(children))

	if len(children) != 0 {
		node.SetExpanded(!node.IsExpanded())
		return
	}

	switch ref := node.GetReference().(type) {
	case discord.GuildID:
		slog.Debug("selected guild - loading channels", "guild_id", ref)

		go discordState.MemberState.Subscribe(ref)

		// Request all members with presence data
		go func() {
			err := discordState.SendGateway(context.TODO(), &gateway.RequestGuildMembersCommand{
				GuildIDs:  []discord.GuildID{ref},
				Limit:     0,
				Presences: true,
			})
			if err != nil {
				slog.Error("failed to request guild members", "guild_id", ref, "err", err)
			}
		}()

		// Update members list for this guild (only if visible)
		if app.chatView.membersList.visible {
			app.chatView.membersList.updateForGuild(ref)
		} else {
			// Just store the guild ID for later
			app.chatView.membersList.currentGuildID = ref
		}

		channels, err := discordState.Cabinet.Channels(ref)
		if err != nil {
			slog.Error("failed to get channels", "err", err, "guild_id", ref)
			return
		}

		slices.SortFunc(channels, func(a, b discord.Channel) int {
			return cmp.Compare(a.Position, b.Position)
		})

		gt.createChannelNodes(node, channels)
		node.SetExpanded(true)
	case discord.ChannelID:
		channel, err := discordState.Cabinet.Channel(ref)
		if err != nil {
			slog.Error("failed to get channel", "channel_id", ref)
			return
		}

		// Hide members list when in DM context
		if channel.Type == discord.DirectMessage || channel.Type == discord.GroupDM {
			if app.chatView.membersList.visible {
				app.chatView.toggleMembersList()
			}
		} else {
			// Update members list for this channel's guild
			if channel.GuildID.IsValid() {
				if app.chatView.membersList.visible {
					app.chatView.membersList.updateForGuild(channel.GuildID)
				} else {
					// Just store the guild ID for later
					app.chatView.membersList.currentGuildID = channel.GuildID
				}
			}
		}

		// Handle forum channels differently - they contain threads, not direct messages
		if channel.Type == discord.GuildForum {
			// Get all channels from the guild - this includes active threads from GuildCreateEvent
			allChannels, err := discordState.Cabinet.Channels(channel.GuildID)
			if err != nil {
				slog.Error("failed to get channels for forum threads", "err", err, "guild_id", channel.GuildID)
				return
			}

			// Filter for threads that belong to this forum channel
			var forumThreads []discord.Channel
			for _, ch := range allChannels {
				if ch.ParentID == channel.ID && (ch.Type == discord.GuildPublicThread ||
					ch.Type == discord.GuildPrivateThread ||
					ch.Type == discord.GuildAnnouncementThread) {
					forumThreads = append(forumThreads, ch)
				}
			}

			// Add threads as child nodes
			for _, thread := range forumThreads {
				gt.createChannelNode(node, thread)
			}

			// Expand the node to show threads
			node.SetExpanded(true)
			return
		}

		// Do everything async to avoid blocking the UI thread
		go func() {
			slog.Info("fetching messages", "channel_id", channel.ID, "limit", gt.cfg.MessagesLimit)
			messages, err := discordState.Messages(channel.ID, uint(gt.cfg.MessagesLimit))
			if err != nil {
				slog.Error("failed to get messages", "err", err, "channel_id", channel.ID, "limit", gt.cfg.MessagesLimit)
				return
			}
			slog.Info("messages fetched", "channel_id", channel.ID, "count", len(messages))

			// Mark channel as read with the actual latest message ID from fetched messages
			if len(messages) > 0 {
				latestMessageID := messages[0].ID
				slog.Debug("marking channel as read", "channel_id", channel.ID, "latest_message_id", latestMessageID)
				discordState.ReadState.MarkRead(channel.ID, latestMessageID)
			}

			if guildID := channel.GuildID; guildID.IsValid() {
				app.chatView.messagesList.requestGuildMembers(guildID, messages)
			}

			hasNoPerm := channel.Type != discord.DirectMessage && channel.Type != discord.GroupDM && !discordState.HasPermissions(channel.ID, discord.PermissionSendMessages)

			// All UI updates must be on UI thread
			app.QueueUpdateDraw(func() {
				slog.Info("drawing messages", "channel_id", channel.ID, "count", len(messages))

				app.chatView.selectedChannel = channel
				app.chatView.messagesList.reset()
				app.chatView.messagesList.setTitle(*channel)
				app.chatView.messagesList.drawMessages(messages)
				app.chatView.messagesList.ScrollToEnd()

				app.chatView.messageInput.SetDisabled(hasNoPerm)
				if hasNoPerm {
					app.chatView.messageInput.SetPlaceholder("You do not have permission to send messages in this channel.")
				} else {
					app.chatView.messageInput.SetPlaceholder("Message...")
					if gt.cfg.AutoFocus {
						app.SetFocus(app.chatView.messageInput)
					}
				}
			})
		}()

		// Update channel style async (don't block onSelected callback)
		go gt.updateChannelStyle(channel.ID, channel.GuildID)

	case nil: // Direct messages folder
		slog.Debug("selected Direct Messages folder - loading DM channels")

		// Load DM channels asynchronously to avoid blocking the UI
		go func() {
			channels, err := discordState.PrivateChannels()
			if err != nil {
				slog.Error("failed to get private channels", "err", err)
				return
			}

			slog.Info("loaded DM channels", "count", len(channels))

			msgID := func(ch discord.Channel) discord.MessageID {
				if ch.LastMessageID.IsValid() {
					return ch.LastMessageID
				}
				return discord.MessageID(ch.ID)
			}

			slices.SortFunc(channels, func(a, b discord.Channel) int {
				// Descending order
				return cmp.Compare(msgID(b), msgID(a))
			})

			// Update UI on the main thread
			app.QueueUpdateDraw(func() {
				// Create all nodes with default style first (fast)
				// Keep references to nodes for style updates
				nodeRefs := make([]*tview.TreeNode, len(channels))
				for i, c := range channels {
					channelNode := tview.NewTreeNode(ui.ChannelToString(c)).
						SetReference(c.ID)
					node.AddChild(channelNode)
					nodeRefs[i] = channelNode
				}
				node.SetExpanded(true)
				slog.Info("DM nodes created", "count", len(channels))

				// Update styles asynchronously in one batch (no expensive Walk operations)
				go func() {
					// Pre-compute all styles off the UI thread
					styles := make([]tcell.Style, len(channels))
					for i, c := range channels {
						styles[i] = gt.getChannelNodeStyle(c.ID)
					}

					// Apply all styles in one UI update
					app.QueueUpdateDraw(func() {
						for i, style := range styles {
							nodeRefs[i].SetTextStyle(style)
						}
						slog.Info("DM styles updated", "count", len(styles))
					})
				}()
			})
		}()

		// Expand immediately to show loading state
		node.SetExpanded(true)
	}
}

func (gt *guildsTree) collapseParentNode(node *tview.TreeNode) {
	gt.
		GetRoot().
		Walk(func(n, parent *tview.TreeNode) bool {
			if n == node && parent.GetLevel() != 0 {
				parent.Collapse()
				gt.SetCurrentNode(parent)
				return false
			}

			return true
		})
}

func (gt *guildsTree) onInputCapture(event *tcell.EventKey) *tcell.EventKey {
	switch event.Name() {
	case gt.cfg.Keys.GuildsTree.CollapseParentNode:
		gt.collapseParentNode(gt.GetCurrentNode())
		return nil
	case gt.cfg.Keys.GuildsTree.MoveToParentNode:
		return tcell.NewEventKey(tcell.KeyRune, "K", tcell.ModNone)

	case gt.cfg.Keys.GuildsTree.SelectPrevious:
		return tcell.NewEventKey(tcell.KeyUp, "", tcell.ModNone)
	case gt.cfg.Keys.GuildsTree.SelectNext:
		return tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone)
	case gt.cfg.Keys.GuildsTree.SelectFirst:
		gt.Move(gt.GetRowCount() * -1)
		// return tcell.NewEventKey(tcell.KeyHome, 0, tcell.ModNone)
	case gt.cfg.Keys.GuildsTree.SelectLast:
		gt.Move(gt.GetRowCount())
		// return tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModNone)

	case gt.cfg.Keys.GuildsTree.SelectCurrent:
		return tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone)

	case gt.cfg.Keys.GuildsTree.YankID:
		gt.yankID()
		return nil

	case gt.cfg.Keys.GuildsTree.CloseDM:
		gt.closeDM()
		return nil
	}

	return nil
}

func (gt *guildsTree) closeDM() {
	node := gt.GetCurrentNode()
	if node == nil {
		return
	}

	// Check if this is a DM channel (not a guild channel)
	ref := node.GetReference()
	channelID, ok := ref.(discord.ChannelID)
	if !ok || !channelID.IsValid() {
		slog.Debug("closeDM: not a valid channel")
		return
	}

	// Get the channel to verify it's a DM
	channel, err := discordState.Cabinet.Channel(channelID)
	if err != nil {
		slog.Error("failed to get channel for closing", "channel_id", channelID, "err", err)
		return
	}

	// Only allow closing DMs, not guild channels
	if channel.Type != discord.DirectMessage && channel.Type != discord.GroupDM {
		slog.Debug("closeDM: not a DM channel", "type", channel.Type)
		return
	}

	slog.Info("closing DM channel", "channel_id", channelID, "channel_name", channel.Name)

	// Find the parent DM node
	var dmNode *tview.TreeNode
	gt.GetRoot().Walk(func(n, parent *tview.TreeNode) bool {
		if n == node && parent != nil {
			dmNode = parent
			return false
		}
		return true
	})

	if dmNode == nil {
		slog.Error("failed to find parent DM node")
		return
	}

	// Remove the channel from the tree
	dmNode.RemoveChild(node)

	// If this was the selected channel, clear the selection
	if app.chatView.selectedChannel != nil && app.chatView.selectedChannel.ID == channelID {
		app.chatView.selectedChannel = nil
		app.chatView.messagesList.reset()
		app.chatView.messageInput.SetDisabled(true)
		app.chatView.messageInput.SetPlaceholder("Select a channel to start chatting")
	}

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

func (gt *guildsTree) yankID() {
	node := gt.GetCurrentNode()
	if node == nil {
		return
	}

	// Reference of a tree node in the guilds tree is its ID.
	// discord.Snowflake (discord.GuildID and discord.ChannelID) have the String method.
	if id, ok := node.GetReference().(fmt.Stringer); ok {
		go clipboard.Write(clipboard.FmtText, []byte(id.String()))
	}
}

func (gt *guildsTree) updateDMStyleAndMove(channelID discord.ChannelID, forceUnread bool) {
	slog.Debug("updating DM style and moving to top", "channel_id", channelID, "force_unread", forceUnread)

	// Find the DM node
	var dmNode *tview.TreeNode
	gt.GetRoot().Walk(func(node, parent *tview.TreeNode) bool {
		if node.GetReference() == channelID {
			dmNode = node
			return false
		}
		return true
	})

	if dmNode == nil {
		slog.Debug("DM node not found", "channel_id", channelID)
		return
	}

	app.QueueUpdateDraw(func() {
		// Force the style to bold (unread)
		if forceUnread {
			dmNode.SetTextStyle(gt.unreadStyle(ningen.ChannelUnread))
			slog.Debug("forced DM to bold/unread", "channel_id", channelID)
		} else {
			dmNode.SetTextStyle(gt.getChannelNodeStyle(channelID))
		}

		// Move to top
		gt.moveDMToTop(dmNode, channelID)
	})
}

func (gt *guildsTree) moveDMToTopOnMessage(channelID discord.ChannelID) {
	slog.Debug("moving DM to top on message", "channel_id", channelID)

	// Find the DM node and the Direct Messages parent
	var dmNode *tview.TreeNode
	gt.GetRoot().Walk(func(node, parent *tview.TreeNode) bool {
		if node.GetReference() == channelID {
			dmNode = node
			return false
		}
		return true
	})

	if dmNode == nil {
		slog.Debug("DM node not found", "channel_id", channelID)
		return
	}

	app.QueueUpdateDraw(func() {
		gt.moveDMToTop(dmNode, channelID)
	})
}

func (gt *guildsTree) moveDMToTop(dmNode *tview.TreeNode, channelID discord.ChannelID) {
	slog.Debug("moving DM to top", "channel_id", channelID)

	// Find the Direct Messages parent node
	var dmParentNode *tview.TreeNode
	gt.GetRoot().Walk(func(node, parent *tview.TreeNode) bool {
		if node.GetText() == "Direct Messages" && parent == gt.GetRoot() {
			dmParentNode = node
			return false
		}
		return true
	})

	if dmParentNode == nil {
		slog.Error("Direct Messages node not found")
		return
	}

	// Find this DM node's current position in the parent's children
	children := dmParentNode.GetChildren()
	var nodeIndex = -1
	for i, child := range children {
		if child == dmNode {
			nodeIndex = i
			break
		}
	}

	if nodeIndex == -1 {
		slog.Error("DM node not found in parent's children")
		return
	}

	// If it's already at the top, nothing to do
	if nodeIndex == 0 {
		return
	}

	// Get a snapshot of all children BEFORE removing any
	allChildren := make([]*tview.TreeNode, len(children))
	copy(allChildren, children)

	// Clear all children
	for _, child := range allChildren {
		dmParentNode.RemoveChild(child)
	}

	// Add the DM node first
	dmParentNode.AddChild(dmNode)

	// Add the rest of the children in their original order
	for _, child := range allChildren {
		if child != dmNode {
			dmParentNode.AddChild(child)
		}
	}

	slog.Debug("DM moved to top", "channel_id", channelID)
}

func (gt *guildsTree) updateChannelStyle(channelID discord.ChannelID, guildID discord.GuildID) {
	slog.Debug("updating channel style", "channel_id", channelID, "guild_id", guildID)

	// Find the channel node and update its style
	if guildID.IsValid() {
		// Guild channel - find the guild node first, then the channel within it
		var guildNode *tview.TreeNode
		gt.GetRoot().Walk(func(node, parent *tview.TreeNode) bool {
			if node.GetReference() == guildID {
				guildNode = node
				return false
			}
			return true
		})

		if guildNode != nil {
			guildNode.Walk(func(node, parent *tview.TreeNode) bool {
				if node.GetReference() == channelID {
					node.SetTextStyle(gt.getChannelNodeStyle(channelID))
					slog.Debug("updated guild channel style", "channel_id", channelID)
					return false
				}
				return true
			})
		}
	} else {
		// DM channel - find it in the Direct Messages node and update style
		gt.GetRoot().Walk(func(node, parent *tview.TreeNode) bool {
			if node.GetReference() == channelID {
				node.SetTextStyle(gt.getChannelNodeStyle(channelID))
				slog.Debug("updated DM channel style", "channel_id", channelID)
				return false
			}
			return true
		})
	}

	// Queue a redraw to show the style change (avoid deadlock)
	app.QueueUpdateDraw(func() {
		// UI update happens in this draw cycle
	})
}
