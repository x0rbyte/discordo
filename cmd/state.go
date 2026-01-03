package cmd

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ayn2op/discordo/internal/http"
	"github.com/ayn2op/discordo/internal/notifications"
	"github.com/ayn2op/tview"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/session"
	"github.com/diamondburned/arikawa/v3/state"
	"github.com/diamondburned/arikawa/v3/state/store/defaultstore"
	"github.com/diamondburned/arikawa/v3/utils/handler"
	"github.com/diamondburned/arikawa/v3/utils/httputil"
	"github.com/diamondburned/arikawa/v3/utils/httputil/httpdriver"
	"github.com/diamondburned/arikawa/v3/utils/ws"
	"github.com/diamondburned/ningen/v3"
	"github.com/diamondburned/ningen/v3/states/read"
)

func openState(token string) error {
	identifyProps := http.IdentifyProperties()
	gateway.DefaultIdentity = identifyProps
	gateway.DefaultPresence = &gateway.UpdatePresenceCommand{
		Status: app.cfg.Status,
	}

	id := gateway.DefaultIdentifier(token)
	id.Compress = false

	session := session.NewCustom(id, http.NewClient(token), handler.New())
	state := state.NewFromSession(session, defaultstore.New())
	discordState = ningen.FromState(state)

	// Handlers
	discordState.AddHandler(onRaw)
	discordState.AddHandler(onReady)
	discordState.AddHandler(onChannelCreate)
	discordState.AddHandler(onMessageCreate)
	discordState.AddHandler(onMessageUpdate)
	discordState.AddHandler(onMessageDelete)
	discordState.AddHandler(onReadUpdate)
	discordState.AddHandler(onGuildMembersChunk)
	discordState.AddHandler(onGuildMemberAdd)
	discordState.AddHandler(onGuildMemberUpdate)
	discordState.AddHandler(onGuildMemberRemove)
	discordState.AddHandler(onPresenceUpdate)
	discordState.AddHandler(onMessageReactionAdd)
	discordState.AddHandler(onMessageReactionRemove)
	discordState.AddHandler(onMessageReactionRemoveAll)

	discordState.AddHandler(func(event *gateway.GuildMembersChunkEvent) {
		app.chatView.messagesList.setFetchingChunk(false, uint(len(event.Members)))
	})

	discordState.AddHandler(func(event *gateway.GuildMemberRemoveEvent) {
		app.chatView.messageInput.cache.Invalidate(event.GuildID.String()+" "+event.User.Username, discordState.MemberState.SearchLimit)
	})

	discordState.StateLog = func(err error) {
		slog.Error("state log", "err", err)
	}

	discordState.OnRequest = append(discordState.OnRequest, httputil.WithHeaders(http.Headers()), onRequest)
	return discordState.Open(context.TODO())
}

func onRequest(r httpdriver.Request) error {
	if req, ok := r.(*httpdriver.DefaultRequest); ok {
		slog.Debug("new HTTP request", "method", req.Method, "url", req.URL)
	}

	return nil
}

func onRaw(event *ws.RawEvent) {
	slog.Debug(
		"new raw event",
		"code", event.OriginalCode,
		"type", event.OriginalType,
		// "data", event.Raw,
	)
}

func onReadUpdate(event *read.UpdateEvent) {
	slog.Debug("READ_STATE_UPDATE received", "channel_id", event.ChannelID, "guild_id", event.GuildID)

	// All tree manipulation must happen on the UI thread
	app.QueueUpdateDraw(func() {
		var guildNode *tview.TreeNode
		var found bool

		app.chatView.guildsTree.
			GetRoot().
			Walk(func(node, parent *tview.TreeNode) bool {
				switch node.GetReference() {
				case event.GuildID:
					node.SetTextStyle(app.chatView.guildsTree.getGuildNodeStyle(event.GuildID))
					guildNode = node
					found = true
					return false
				case event.ChannelID:
					// private channel
					if !event.GuildID.IsValid() {
						style := app.chatView.guildsTree.getChannelNodeStyle(event.ChannelID)
						node.SetTextStyle(style)
						found = true
						return false
					}
				}

				return true
			})

		if guildNode != nil && guildNode.IsExpanded() {
			guildNode.Walk(func(node, parent *tview.TreeNode) bool {
				if node.GetReference() == event.ChannelID {
					node.SetTextStyle(app.chatView.guildsTree.getChannelNodeStyle(event.ChannelID))
					found = true
					return false
				}

				return true
			})
		}

		if found {
			slog.Debug("updated style for read state", "channel_id", event.ChannelID, "guild_id", event.GuildID)
		}
	})
}

func onChannelCreate(event *gateway.ChannelCreateEvent) {
	// Only handle DM channels
	if event.Type != discord.DirectMessage && event.Type != discord.GroupDM {
		return
	}

	// All tree manipulation must happen on the UI thread
	app.QueueUpdateDraw(func() {
		// Find the "Direct Messages" node
		var dmNode *tview.TreeNode
		app.chatView.guildsTree.
			GetRoot().
			Walk(func(node, parent *tview.TreeNode) bool {
				// Check for "Direct Messages" text, not just nil reference (folders also have nil ref)
				if node.GetText() == "Direct Messages" && parent == app.chatView.guildsTree.GetRoot() {
					dmNode = node
					return false
				}
				return true
			})

		if dmNode == nil {
			return
		}

		// Check if this channel already exists in the tree
		var exists bool
		dmNode.Walk(func(node, parent *tview.TreeNode) bool {
			if node.GetReference() == event.ID {
				exists = true
				return false
			}
			return true
		})

		// If channel doesn't exist, add it
		if !exists {
			app.chatView.guildsTree.createChannelNode(dmNode, event.Channel)
		}
	})
}

var guildsTreeInitialized bool

func onReady(r *gateway.ReadyEvent) {
	slog.Info("onReady event received", "already_initialized", guildsTreeInitialized)

	// Only build the tree once - don't rebuild on subsequent Ready events (reconnections)
	if guildsTreeInitialized {
		slog.Warn("IGNORING Ready event - tree already initialized, this is a reconnection")
		return
	}

	slog.Info("Building guilds tree from Ready event")
	guildsTreeInitialized = true

	root := app.chatView.guildsTree.GetRoot()
	dmNode := tview.NewTreeNode("Direct Messages")
	root.ClearChildren().AddChild(dmNode)

	for _, folder := range r.UserSettings.GuildFolders {
		if folder.ID == 0 && len(folder.GuildIDs) == 1 {
			guild, err := discordState.Cabinet.Guild(folder.GuildIDs[0])
			if err != nil {
				slog.Error(
					"failed to get guild from state",
					"guild_id",
					folder.GuildIDs[0],
					"err",
					err,
				)
				continue
			}

			app.chatView.guildsTree.createGuildNode(root, *guild)
		} else {
			app.chatView.guildsTree.createFolderNode(folder)
		}
	}

	app.chatView.guildsTree.SetCurrentNode(root)
	app.SetFocus(app.chatView.guildsTree)
	app.Draw()
}

func onMessageCreate(message *gateway.MessageCreateEvent) {
	isCurrentChannel := app.chatView.selectedChannel != nil &&
		app.chatView.selectedChannel.ID == message.ChannelID

	if isCurrentChannel {
		app.chatView.messagesList.drawMessage(app.chatView.messagesList, message.Message)
		app.Draw()

		// Auto-mark as read when viewing the channel
		go discordState.ReadState.MarkRead(message.ChannelID, message.ID)
	}

	if err := notifications.Notify(discordState, message, app.cfg); err != nil {
		slog.Error("failed to notify", "err", err, "channel_id", message.ChannelID, "message_id", message.ID)
	}

	// Check if this is a DM and handle it specially
	channel, err := discordState.Cabinet.Channel(message.ChannelID)
	isDM := err == nil && (channel.Type == discord.DirectMessage || channel.Type == discord.GroupDM)

	if isDM {
		// For DMs, always bold and move to top when message arrives (unless currently viewing)
		if !isCurrentChannel {
			go app.chatView.guildsTree.updateDMStyleAndMove(message.ChannelID, true)
		} else {
			go app.chatView.guildsTree.moveDMToTopOnMessage(message.ChannelID)
		}
	} else {
		// For guild channels, update style based on read state
		go app.chatView.guildsTree.updateChannelStyle(message.ChannelID, message.GuildID)
	}
}

func onMessageUpdate(message *gateway.MessageUpdateEvent) {
	if app.chatView.selectedChannel != nil &&
		app.chatView.selectedChannel.ID == message.ChannelID {
		onMessageDelete(&gateway.MessageDeleteEvent{ID: message.ID, ChannelID: message.ChannelID, GuildID: message.GuildID})
	}
}

func onMessageDelete(message *gateway.MessageDeleteEvent) {
	if app.chatView.selectedChannel != nil &&
		app.chatView.selectedChannel.ID == message.ChannelID {
		messages, err := discordState.Cabinet.Messages(message.ChannelID)
		if err != nil {
			slog.Error("failed to get messages from state", "err", err, "channel_id", message.ChannelID)
			return
		}

		app.QueueUpdateDraw(func() {
			app.chatView.messagesList.reset()
			app.chatView.messagesList.drawMessages(messages)
		})
	}
}

func onGuildMembersChunk(event *gateway.GuildMembersChunkEvent) {
	if app.chatView.membersList.currentGuildID == event.GuildID && app.chatView.membersList.visible {
		app.QueueUpdateDraw(func() {
			app.chatView.membersList.rebuildList()
		})
	}
}

func onGuildMemberAdd(event *gateway.GuildMemberAddEvent) {
	if app.chatView.membersList.currentGuildID == event.GuildID && app.chatView.membersList.visible {
		app.QueueUpdateDraw(func() {
			app.chatView.membersList.rebuildList()
		})
	}
}

func onGuildMemberUpdate(event *gateway.GuildMemberUpdateEvent) {
	if app.chatView.membersList.currentGuildID == event.GuildID && app.chatView.membersList.visible {
		app.QueueUpdateDraw(func() {
			app.chatView.membersList.rebuildList()
		})
	}
}

func onGuildMemberRemove(event *gateway.GuildMemberRemoveEvent) {
	if app.chatView.membersList.currentGuildID == event.GuildID && app.chatView.membersList.visible {
		app.QueueUpdateDraw(func() {
			app.chatView.membersList.rebuildList()
		})
	}
}

func onPresenceUpdate(event *gateway.PresenceUpdateEvent) {
	if app.chatView.membersList.currentGuildID == event.GuildID && app.chatView.membersList.visible {
		app.QueueUpdateDraw(func() {
			app.chatView.membersList.updateMemberPresence(event.User.ID)
		})
	}
}

func onMessageReactionAdd(event *gateway.MessageReactionAddEvent) {
	if app.chatView.selectedChannel != nil &&
		app.chatView.selectedChannel.ID == event.ChannelID {

		messages, err := discordState.Cabinet.Messages(event.ChannelID)
		if err != nil {
			slog.Error("failed to get messages after reaction add", "err", err)
			return
		}

		app.QueueUpdateDraw(func() {
			app.chatView.messagesList.reset()
			app.chatView.messagesList.drawMessages(messages)
		})
	}
}

func onMessageReactionRemove(event *gateway.MessageReactionRemoveEvent) {
	if app.chatView.selectedChannel != nil &&
		app.chatView.selectedChannel.ID == event.ChannelID {

		messages, err := discordState.Cabinet.Messages(event.ChannelID)
		if err != nil {
			slog.Error("failed to get messages after reaction remove", "err", err)
			return
		}

		app.QueueUpdateDraw(func() {
			app.chatView.messagesList.reset()
			app.chatView.messagesList.drawMessages(messages)
		})
	}
}

func onMessageReactionRemoveAll(event *gateway.MessageReactionRemoveAllEvent) {
	if app.chatView.selectedChannel != nil &&
		app.chatView.selectedChannel.ID == event.ChannelID {

		messages, err := discordState.Cabinet.Messages(event.ChannelID)
		if err != nil {
			slog.Error("failed to get messages after reactions cleared", "err", err)
			return
		}

		app.QueueUpdateDraw(func() {
			app.chatView.messagesList.reset()
			app.chatView.messagesList.drawMessages(messages)
		})
	}
}

func initiateDM(userID discord.UserID) error {
	// Create or get existing DM channel
	channel, err := discordState.CreatePrivateChannel(userID)
	if err != nil {
		return fmt.Errorf("failed to create DM channel: %w", err)
	}

	slog.Info("initiating DM", "channel_id", channel.ID, "user_id", userID)

	// Load messages asynchronously
	go func() {
		messages, err := discordState.Messages(channel.ID, uint(app.cfg.MessagesLimit))
		if err != nil {
			slog.Error("failed to get DM messages", "err", err, "channel_id", channel.ID)
			return
		}

		// All UI operations must be on UI thread
		app.QueueUpdateDraw(func() {
			// Find DM node in tree
			var dmNode *tview.TreeNode
			app.chatView.guildsTree.GetRoot().Walk(func(node, parent *tview.TreeNode) bool {
				// Check for "Direct Messages" text, not just nil reference (folders also have nil ref)
				if node.GetText() == "Direct Messages" && parent == app.chatView.guildsTree.GetRoot() {
					dmNode = node
					return false
				}
				return true
			})

			if dmNode == nil {
				slog.Error("DM node not found in guilds tree")
				return
			}

			// Check if channel already exists in tree
			var exists bool
			slog.Debug("checking if DM already exists in tree", "channel_id", channel.ID, "dm_children", len(dmNode.GetChildren()))
			dmNode.Walk(func(node, parent *tview.TreeNode) bool {
				if node.GetReference() == channel.ID {
					slog.Info("DM channel already exists in tree", "channel_id", channel.ID)
					exists = true
					return false
				}
				return true
			})

			// Add channel to tree if not exists
			if !exists {
				slog.Info("adding new DM to tree", "channel_id", channel.ID)
				app.chatView.guildsTree.createChannelNode(dmNode, *channel)
				dmNode.SetExpanded(true)
				slog.Info("DM added to tree", "channel_id", channel.ID, "dm_children_now", len(dmNode.GetChildren()))
			} else {
				slog.Info("DM already in tree, not adding", "channel_id", channel.ID)
			}

			// Select the channel and display messages
			app.chatView.selectedChannel = channel
			app.chatView.messagesList.reset()
			app.chatView.messagesList.setTitle(*channel)
			app.chatView.messagesList.drawMessages(messages)
			app.chatView.messagesList.ScrollToEnd()
			app.chatView.messageInput.SetDisabled(false)
			app.chatView.messageInput.SetPlaceholder("Message...")

			if app.cfg.AutoFocus {
				app.SetFocus(app.chatView.messageInput)
			}
		})
	}()

	return nil
}
