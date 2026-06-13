package whatsapp

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/msfoundry/commit/store"

	_ "modernc.org/sqlite"
)

type Extractor interface {
	StartProcessingLoop(ctx context.Context)
}

type Client struct {
	db        *store.DB
	dataDir   string
	extractor Extractor

	mu           sync.RWMutex
	wa           *whatsmeow.Client
	container    *sqlstore.Container
	qrChan       chan string
	connected    bool
	appCtx       context.Context
	loopsStarted bool
	readOnly     bool // when true, the client can NEVER send a WhatsApp message

	pendingMu      sync.Mutex
	pendingChoices []string // person names awaiting disambiguation
}

func New(db *store.DB, dataDir string, extractor Extractor, appCtx context.Context) *Client {
	return &Client{
		db:        db,
		dataDir:   dataDir,
		extractor: extractor,
		qrChan:    make(chan string, 5),
		appCtx:    appCtx,
		readOnly:  true, // Outscroll fork: observe + draft only, never send. Safest for ban risk.
	}
}

// IsReadOnly reports whether sending is disabled. True for this fork.
func (c *Client) IsReadOnly() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.readOnly
}

func (c *Client) HasSession() bool {
	container, err := c.getContainer()
	if err != nil {
		return false
	}
	devices, err := container.GetAllDevices(context.Background())
	if err != nil {
		return false
	}
	return len(devices) > 0
}

func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

func (c *Client) QRChannel() <-chan string {
	return c.qrChan
}

func (c *Client) Connect(ctx context.Context) error {
	container, err := c.getContainer()
	if err != nil {
		return fmt.Errorf("get container: %w", err)
	}

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("get device: %w", err)
	}

	client := whatsmeow.NewClient(deviceStore, waLog.Noop)
	c.mu.Lock()
	c.wa = client
	c.mu.Unlock()

	client.AddEventHandler(c.handleEvent)

	if client.Store.ID == nil {
		qrChan, _ := client.GetQRChannel(ctx)
		if err := client.Connect(); err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		for evt := range qrChan {
			if evt.Event == "code" {
				select {
				case c.qrChan <- evt.Code:
				default:
				}
			}
		}
	} else {
		if err := client.Connect(); err != nil {
			return fmt.Errorf("connect: %w", err)
		}
	}

	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()

	c.startLoops(ctx)

	<-ctx.Done()
	client.Disconnect()
	return nil
}

func (c *Client) Login(ctx context.Context) (<-chan string, error) {
	container, err := c.getContainer()
	if err != nil {
		return nil, fmt.Errorf("get container: %w", err)
	}

	deviceStore := container.NewDevice()
	client := whatsmeow.NewClient(deviceStore, waLog.Noop)

	c.mu.Lock()
	c.wa = client
	c.mu.Unlock()

	client.AddEventHandler(c.handleEvent)

	qrCodes := make(chan string, 5)

	go func() {
		defer close(qrCodes)
		qrChan, _ := client.GetQRChannel(ctx)
		if err := client.Connect(); err != nil {
			log.Printf("connect error: %v", err)
			return
		}
		for evt := range qrChan {
			if evt.Event == "code" {
				select {
				case qrCodes <- evt.Code:
				default:
				}
			} else if evt.Event == "success" {
				c.mu.Lock()
				c.connected = true
				c.mu.Unlock()
				c.startLoops(c.appCtx)
				return
			}
		}
	}()

	return qrCodes, nil
}

func (c *Client) handleEvent(rawEvt interface{}) {
	switch evt := rawEvt.(type) {
	case *events.Message:
		// Read-only: never auto-respond or parse incoming as bot commands —
		// just record the message for analysis.
		if c.IsReadOnly() || !c.handleBotCommand(context.Background(), evt) {
			c.handleMessage(evt)
		}
	case *events.HistorySync:
		go c.handleHistorySync(evt)
	case *events.Connected:
		log.Println("WhatsApp connected")
		c.mu.Lock()
		c.connected = true
		c.mu.Unlock()
	case *events.Disconnected:
		log.Println("WhatsApp disconnected")
		c.mu.Lock()
		c.connected = false
		client := c.wa
		c.mu.Unlock()
		if client != nil {
			go c.reconnect(client)
		}
	}
}

func (c *Client) reconnect(client *whatsmeow.Client) {
	backoff := 5 * time.Second
	maxBackoff := 5 * time.Minute
	for {
		select {
		case <-c.appCtx.Done():
			return
		case <-time.After(backoff):
		}

		c.mu.RLock()
		current := c.wa
		c.mu.RUnlock()
		if current != client {
			return
		}

		log.Printf("attempting WhatsApp reconnect...")
		if err := client.Connect(); err != nil {
			log.Printf("reconnect failed: %v", err)
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		log.Println("WhatsApp reconnected")
		return
	}
}

func (c *Client) handleMessage(evt *events.Message) {
	text := extractText(evt.Message)
	if text == "" {
		return
	}

	chatJID := evt.Info.Chat.String()
	if evt.Info.Chat.Server == types.BroadcastServer {
		return
	}
	if c.db.IsChatMuted(chatJID) {
		return
	}
	senderJID := evt.Info.Sender.String()
	isGroup := evt.Info.Chat.Server == types.GroupServer
	isFromMe := evt.Info.IsFromMe

	senderName := ""
	chatName := ""
	if evt.Info.PushName != "" {
		senderName = evt.Info.PushName
	}
	if isGroup {
		chatName = c.getChatName(evt.Info.Chat)
	} else if isFromMe {
		chatName = c.db.GetChatDisplayName(chatJID)
	} else {
		chatName = senderName
	}

	msg := &store.Message{
		ID:         evt.Info.ID,
		ChatJID:    chatJID,
		SenderJID:  senderJID,
		SenderName: senderName,
		ChatName:   chatName,
		Content:    text,
		Timestamp:  evt.Info.Timestamp,
		IsFromMe:   isFromMe,
		IsGroup:    isGroup,
		MentionsMe: c.messageMentionsMe(evt.Message),
	}

	if err := c.db.SaveMessage(msg); err != nil {
		log.Printf("save message error: %v", err)
	}
}

func (c *Client) getChatName(jid types.JID) string {
	c.mu.RLock()
	client := c.wa
	c.mu.RUnlock()

	if client == nil {
		return jid.String()
	}

	info, err := client.GetGroupInfo(context.Background(), jid)
	if err != nil {
		return jid.String()
	}
	return info.Name
}

// ErrReadOnly is returned by all send paths when the client is in read-only
// mode. Nothing reaches WhatsApp's servers.
var ErrReadOnly = fmt.Errorf("read-only mode: sending is disabled")

func (c *Client) SendMessage(ctx context.Context, jid types.JID, text string) error {
	c.mu.RLock()
	client := c.wa
	readOnly := c.readOnly
	c.mu.RUnlock()

	// HARD GATE: in read-only mode the app never transmits anything. This is the
	// single chokepoint every send path (dashboard reply, reminders, welcome
	// message, rate-limit notice, bot replies) funnels through.
	if readOnly {
		log.Printf("read-only mode: suppressed outbound message to %s", jid.String())
		return ErrReadOnly
	}

	if client == nil {
		return fmt.Errorf("not connected")
	}

	_, err := client.SendMessage(ctx, jid, &waE2E.Message{
		Conversation: &text,
	})
	return err
}

func (c *Client) Notify(text string) {
	ownJID := c.GetOwnJID()
	if ownJID.IsEmpty() {
		return
	}
	selfJID := types.NewJID(ownJID.User, types.DefaultUserServer)
	if err := c.SendMessage(c.appCtx, selfJID, text); err != nil {
		log.Printf("notify error: %v", err)
	}
}

func (c *Client) SendWelcomeMessages(ctx context.Context, onStage func(stage string)) {
	ownJID := c.GetOwnJID()
	if !ownJID.IsEmpty() {
		selfJID := types.NewJID(ownJID.User, types.DefaultUserServer)
		_ = c.SendMessage(ctx, selfJID, "✅ Connected to Commit. Your dashboard is ready.")
	}

	stages := []string{"connected", "scanning", "ready"}
	for _, s := range stages {
		if onStage != nil {
			onStage(s)
		}
	}
}

func (c *Client) isSelfChat(evt *events.Message) bool {
	chat := evt.Info.Chat
	sender := evt.Info.Sender

	// Old format: chat is your phone number @s.whatsapp.net
	ownJID := c.GetOwnJID()
	if !ownJID.IsEmpty() && chat.User == ownJID.User {
		return true
	}

	// New LID format: self-chat is your LID @lid, sender LID matches chat
	if chat.Server == types.HiddenUserServer && sender.User == chat.User {
		return true
	}

	return false
}

func (c *Client) GetOwnJID() types.JID {
	c.mu.RLock()
	client := c.wa
	c.mu.RUnlock()

	if client == nil || client.Store.ID == nil {
		return types.JID{}
	}
	return *client.Store.ID
}

func (c *Client) startLoops(ctx context.Context) {
	c.mu.Lock()
	if c.loopsStarted {
		c.mu.Unlock()
		return
	}
	c.loopsStarted = true
	readOnly := c.readOnly
	c.mu.Unlock()
	go c.extractor.StartProcessingLoop(ctx) // read + analyze only, no sends
	go c.nameResolverLoop(ctx)              // fill real contact names (kills "Unknown")
	if !readOnly {
		go c.reminderLoop(ctx) // reminderLoop delivers via WhatsApp; pointless (and blocked) in read-only
	}
}

// resolveContactName looks up a 1:1 contact's display name from WhatsApp's
// synced contact store (your address-book name, their push name, etc.).
func (c *Client) resolveContactName(jid types.JID) string {
	c.mu.RLock()
	client := c.wa
	c.mu.RUnlock()
	if client == nil || client.Store == nil || client.Store.Contacts == nil {
		return ""
	}
	info, err := client.Store.Contacts.GetContact(context.Background(), jid)
	if err != nil || !info.Found {
		return ""
	}
	// Prefer a real human name; never let a masked/redacted phone ("+91•••38")
	// become the display name.
	for _, cand := range []string{info.FullName, info.FirstName, info.BusinessName, info.PushName} {
		if looksLikeName(cand) {
			return cand
		}
	}
	return ""
}

// looksLikeName rejects empty, masked ("•"), or phone-shaped strings — only
// something with an actual letter counts as a name.
func looksLikeName(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "•") {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

// isArchived reports whether WhatsApp has this chat archived (synced via
// app-state). Mirrors your phone's archive into Orbit.
func (c *Client) isArchived(jid types.JID) bool {
	c.mu.RLock()
	client := c.wa
	c.mu.RUnlock()
	if client == nil || client.Store == nil || client.Store.ChatSettings == nil {
		return false
	}
	s, err := client.Store.ChatSettings.GetChatSettings(context.Background(), jid)
	if err != nil {
		return false
	}
	return s.Found && s.Archived
}

// nameResolverLoop periodically syncs WhatsApp-side state into Orbit: real
// contact names (1:1) and which chats you've archived. Runs a bit after connect
// (so app-state sync has landed) and every 10 min after.
func (c *Client) nameResolverLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(15 * time.Second):
	}
	for {
		// Resolve real names for 1:1 chats.
		if jids, err := c.db.DistinctPersonChats(); err == nil {
			for _, j := range jids {
				if jid, err := types.ParseJID(j); err == nil {
					if name := c.resolveContactName(jid); name != "" {
						c.db.UpdateChatName(j, name)
					}
				}
			}
		}
		// Mirror archive state across all chats (1:1 + groups).
		if jids, err := c.db.AllChatJIDs(); err == nil {
			archived := []string{}
			for _, j := range jids {
				if jid, err := types.ParseJID(j); err == nil && c.isArchived(jid) {
					archived = append(archived, j)
				}
			}
			c.db.ReplaceArchived(archived)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Minute):
		}
	}
}

func (c *Client) getContainer() (*sqlstore.Container, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.container != nil {
		return c.container, nil
	}
	dbPath := filepath.Join(c.dataDir, "whatsmeow.db")
	container, err := sqlstore.New(context.Background(), "sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", dbPath), waLog.Noop)
	if err != nil {
		return nil, err
	}
	c.container = container
	return container, nil
}

// messageMentionsMe reports whether this message @-mentions the linked user
// (matches own phone JID or LID). A strong local priority signal — no AI.
func (c *Client) messageMentionsMe(msg *waE2E.Message) bool {
	if msg == nil || msg.ExtendedTextMessage == nil {
		return false
	}
	ci := msg.ExtendedTextMessage.GetContextInfo()
	if ci == nil {
		return false
	}
	mentioned := ci.GetMentionedJID()
	if len(mentioned) == 0 {
		return false
	}
	c.mu.RLock()
	client := c.wa
	c.mu.RUnlock()
	if client == nil || client.Store == nil {
		return false
	}
	var mine []string
	if client.Store.ID != nil {
		mine = append(mine, client.Store.ID.User)
	}
	if !client.Store.LID.IsEmpty() {
		mine = append(mine, client.Store.LID.User)
	}
	for _, mj := range mentioned {
		jid, err := types.ParseJID(mj)
		if err != nil {
			continue
		}
		for _, u := range mine {
			if u != "" && jid.User == u {
				return true
			}
		}
	}
	return false
}

func extractText(msg *waE2E.Message) string {
	if msg == nil {
		return ""
	}
	if msg.Conversation != nil {
		return *msg.Conversation
	}
	if msg.ExtendedTextMessage != nil && msg.ExtendedTextMessage.Text != nil {
		return *msg.ExtendedTextMessage.Text
	}
	// Non-text messages: store a labelled placeholder (+ caption) so the
	// timeline and the who-owes-a-reply queues stay correct. A photo/voice
	// reply must count as "they replied" — otherwise the direction inverts.
	if m := msg.ImageMessage; m != nil {
		return withCaption("📷 Photo", m.GetCaption())
	}
	if m := msg.VideoMessage; m != nil {
		return withCaption("🎥 Video", m.GetCaption())
	}
	if m := msg.AudioMessage; m != nil {
		if m.GetPTT() {
			return "🎤 Voice message"
		}
		return "🎵 Audio"
	}
	if m := msg.DocumentMessage; m != nil {
		label := "📄 Document"
		if m.GetFileName() != "" {
			label = "📄 " + m.GetFileName()
		}
		return withCaption(label, m.GetCaption())
	}
	if msg.StickerMessage != nil {
		return "🌟 Sticker"
	}
	if msg.LocationMessage != nil || msg.LiveLocationMessage != nil {
		return "📍 Location"
	}
	if msg.ContactMessage != nil || msg.ContactsArrayMessage != nil {
		return "👤 Contact"
	}
	// Reactions, edits, deletes, polls, receipts, etc. are intentionally
	// ignored (return "") so they don't pollute the message timeline.
	return ""
}

func withCaption(label, caption string) string {
	if caption != "" {
		return label + ": " + caption
	}
	return label
}

func (c *Client) reminderLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			due, err := c.db.GetDueReminders()
			if err != nil {
				log.Printf("reminder check error: %v", err)
				continue
			}
			for _, cm := range due {
				direction := "You promised"
				if cm.Direction == "they_owe" {
					direction = cm.PersonName + " promised"
				}
				text := fmt.Sprintf("⏰ Reminder: %s — %s\n\n%s", cm.Title, direction, cm.Context)

				ownJID := c.GetOwnJID()
				if !ownJID.IsEmpty() {
					selfJID := types.NewJID(ownJID.User, types.DefaultUserServer)
					if err := c.SendMessage(ctx, selfJID, text); err != nil {
						log.Printf("send reminder error: %v", err)
						continue
					}
				}
				c.db.ClearReminder(cm.ID)
			}
		}
	}
}

// Logout disconnects and removes the session
func (c *Client) Logout() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.wa != nil {
		c.wa.Disconnect()
		c.wa = nil
	}
	c.connected = false

	dbPath := filepath.Join(c.dataDir, "whatsmeow.db")
	os.Remove(dbPath)
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")
	return nil
}

func (c *Client) handleHistorySync(evt *events.HistorySync) {
	data := evt.Data
	if data == nil {
		return
	}
	conversations := data.GetConversations()
	if len(conversations) == 0 {
		return
	}

	count := 0
	for _, conv := range conversations {
		chatJID := conv.GetID()
		if chatJID == "" || chatJID == "status@broadcast" {
			continue
		}
		if c.db.IsChatMuted(chatJID) {
			continue
		}
		isGroup := strings.HasSuffix(chatJID, "@g.us")
		chatName := conv.GetDisplayName()
		if chatName == "" {
			chatName = conv.GetName()
		}

		for _, histMsg := range conv.GetMessages() {
			webMsg := histMsg.GetMessage()
			if webMsg == nil || webMsg.GetMessage() == nil {
				continue
			}
			key := webMsg.GetKey()
			if key == nil {
				continue
			}

			text := extractText(webMsg.GetMessage())
			if text == "" {
				continue
			}

			ts := webMsg.GetMessageTimestamp()
			if ts == 0 {
				continue
			}
			msgTime := time.Unix(int64(ts), 0)
			if msgTime.Before(time.Now().AddDate(0, 0, -60)) { // backfill 60 days so day-one isn't empty
				continue
			}

			senderName := webMsg.GetPushName()
			isFromMe := key.GetFromMe()
			senderJID := key.GetParticipant()
			if senderJID == "" && !isGroup {
				if isFromMe {
					ownJID := c.GetOwnJID()
					if !ownJID.IsEmpty() {
						senderJID = ownJID.String()
					}
				} else {
					senderJID = chatJID
				}
			}

			if chatName == "" && !isGroup && !isFromMe {
				chatName = senderName
			}

			msg := &store.Message{
				ID:         key.GetID(),
				ChatJID:    chatJID,
				SenderJID:  senderJID,
				SenderName: senderName,
				ChatName:   chatName,
				Content:    text,
				Timestamp:  msgTime,
				IsFromMe:   isFromMe,
				IsGroup:    isGroup,
				MentionsMe: c.messageMentionsMe(webMsg.GetMessage()),
			}
			if err := c.db.SaveMessage(msg); err == nil {
				count++
			}
		}
	}
	if count > 0 {
		log.Printf("history sync: saved %d messages from %d conversations", count, len(conversations))
	}
}

