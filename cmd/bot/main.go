package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"autobots/pkg/protocol"
	"autobots/pkg/queue"
	"autobots/pkg/registry"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

type BotServer struct {
	pageAccessToken string
	verifyToken     string
	queue           *queue.Queue
	registry        *registry.Registry
}

// MessengerWebhook - Cấu trúc webhook từ Facebook
type MessengerWebhook struct {
	Object string         `json:"object"`
	Entry  []WebhookEntry `json:"entry"`
}

type WebhookEntry struct {
	ID        string           `json:"id"`
	Time      int64            `json:"time"`
	Messaging []MessagingEvent `json:"messaging"`
}

type MessagingEvent struct {
	Sender    User     `json:"sender"`
	Recipient User     `json:"recipient"`
	Timestamp int64    `json:"timestamp"`
	Message   *Message `json:"message,omitempty"`
}

type User struct {
	ID string `json:"id"`
}

type Message struct {
	Mid         string       `json:"mid"`
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

type Attachment struct {
	Type    string            `json:"type"`
	Payload AttachmentPayload `json:"payload"`
}

type AttachmentPayload struct {
	URL string `json:"url"`
}

func main() {
	// Load config
	// if err := godotenv.Load("../../config/.env"); err != nil {
	// 	log.Println("Không tìm thấy file .env, dùng biến môi trường")
	// }

	pageAccessToken := os.Getenv("PAGE_ACCESS_TOKEN")
	verifyToken := os.Getenv("VERIFY_TOKEN")
	redisHost := getEnv("REDIS_HOST", "localhost")
	redisPort := getEnv("REDIS_PORT", "6379")
	redisPassword := getEnv("REDIS_PASSWORD", "")
	botPort := getEnv("BOT_PORT", "8080")

	if pageAccessToken == "" || verifyToken == "" {
		log.Fatal("Thiếu PAGE_ACCESS_TOKEN hoặc VERIFY_TOKEN")
	}

	// Khởi tạo queue
	q, err := queue.NewQueue(redisHost, redisPort, redisPassword)
	if err != nil {
		log.Fatalf("Không thể kết nối queue: %v", err)
	}
	defer q.Close()

	// Khởi tạo registry
	reg, err := registry.NewRegistry(redisHost, redisPort, redisPassword, 60)
	if err != nil {
		log.Fatalf("Không thể kết nối registry: %v", err)
	}
	defer reg.Close()

	bot := &BotServer{
		pageAccessToken: pageAccessToken,
		verifyToken:     verifyToken,
		queue:           q,
		registry:        reg,
	}

	// Setup router
	r := mux.NewRouter()
	r.HandleFunc("/webhook", bot.handleWebhookVerify).Methods("GET")
	r.HandleFunc("/webhook", bot.handleWebhook).Methods("POST")
	r.HandleFunc("/health", bot.handleHealth).Methods("GET")
	r.HandleFunc("/plugins", bot.handleListPlugins).Methods("GET")

	// Lắng nghe response từ plugins
	go bot.listenPluginResponses()

	log.Printf("🤖 Autobots Bot Server đang chạy trên port %s", botPort)
	log.Printf("📡 Webhook URL: http://localhost:%s/webhook", botPort)
	log.Fatal(http.ListenAndServe(":"+botPort, r))
}

// handleWebhookVerify - Xác thực webhook với Facebook
func (b *BotServer) handleWebhookVerify(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if mode == "subscribe" && token == b.verifyToken {
		log.Println("✅ Webhook verified!")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(challenge))
		return
	}

	log.Println("❌ Webhook verification failed")
	w.WriteHeader(http.StatusForbidden)
}

// handleWebhook - Nhận message từ Messenger
func (b *BotServer) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Lỗi đọc body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var webhook MessengerWebhook
	if err := json.Unmarshal(body, &webhook); err != nil {
		log.Printf("Lỗi parse JSON: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Xử lý các message
	for _, entry := range webhook.Entry {
		for _, event := range entry.Messaging {
			if event.Message != nil {
				go b.handleMessage(event)
			}
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("EVENT_RECEIVED"))
}

// handleMessage - Xử lý message
func (b *BotServer) handleMessage(event MessagingEvent) {
	userID := event.Sender.ID
	text := event.Message.Text

	log.Printf("📩 Nhận message từ %s: %s", userID, text)

	// Parse command
	parts := strings.Fields(text)
	if len(parts) == 0 {
		b.sendMessage(userID, "Xin chào! Tôi là Autobots 🤖\nGửi /help để xem danh sách lệnh.")
		return
	}

	command := strings.TrimPrefix(parts[0], "/")
	args := parts[1:]

	// Xử lý các lệnh đặc biệt
	switch command {
	case "help":
		b.handleHelp(userID)
		return
	case "start":
		b.sendMessage(userID, "🤖 Autobots, roll out!\nGửi /help để xem lệnh.")
		return
	case "plugins":
		b.handlePluginsList(userID)
		return
	}

	// Tìm plugin xử lý command
	ctx := context.Background()
	plugin, err := b.registry.FindPluginByCommand(ctx, command)
	if err != nil {
		b.sendMessage(userID, fmt.Sprintf("❌ Không tìm thấy plugin cho lệnh: /%s", command))
		return
	}

	// Tạo request gửi đến plugin
	requestID := uuid.New().String()

	// Parse file attachments
	var files []protocol.FileAttachment
	if event.Message.Attachments != nil {
		for _, att := range event.Message.Attachments {
			files = append(files, protocol.FileAttachment{
				URL:  att.Payload.URL,
				Type: att.Type,
			})
		}
	}

	request := protocol.PluginRequest{
		RequestID: requestID,
		Command:   command,
		Args:      args,
		UserID:    userID,
		Files:     files,
		CreatedAt: time.Now(),
	}

	// Gửi vào queue của plugin
	if err := b.queue.Publish(ctx, plugin.QueueName, request); err != nil {
		log.Printf("Lỗi gửi message đến plugin: %v", err)
		b.sendMessage(userID, "❌ Lỗi khi gửi yêu cầu đến plugin")
		return
	}

	b.sendMessage(userID, fmt.Sprintf("⏳ Đang xử lý bởi %s...", plugin.Name))
	log.Printf("✅ Đã gửi request %s đến plugin %s", requestID, plugin.Name)
}

// listenPluginResponses - Lắng nghe response từ plugins
func (b *BotServer) listenPluginResponses() {
	ctx := context.Background()
	queueName := "autobots:responses"

	log.Println("👂 Đang lắng nghe responses từ plugins...")

	err := b.queue.Subscribe(ctx, queueName, func(data []byte) error {
		var response protocol.PluginResponse
		if err := json.Unmarshal(data, &response); err != nil {
			return fmt.Errorf("lỗi parse response: %w", err)
		}

		log.Printf("📨 Nhận response từ plugin: %s (status: %s)", response.RequestID, response.Status)

		// Gửi kết quả về user
		if response.Status == "success" {
			b.sendMessage(response.Metadata["user_id"].(string), "✅ "+response.Result)
		} else {
			b.sendMessage(response.Metadata["user_id"].(string), "❌ "+response.Error)
		}

		return nil
	})

	if err != nil {
		log.Printf("Lỗi khi subscribe responses: %v", err)
	}
}

// sendMessage - Gửi message đến user
func (b *BotServer) sendMessage(userID, text string) error {
	url := "https://graph.facebook.com/v18.0/me/messages"

	payload := map[string]interface{}{
		"recipient": map[string]string{"id": userID},
		"message":   map[string]string{"text": text},
	}

	jsonData, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.URL.RawQuery = "access_token=" + b.pageAccessToken

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Lỗi gửi message: %s", string(body))
	}

	return nil
}

// handleHelp - Hiển thị help
func (b *BotServer) handleHelp(userID string) {
	help := `🤖 *AUTOBOTS - Danh sách lệnh*

/help - Hiển thị trợ giúp
/plugins - Danh sách plugins
/start - Khởi động bot

Các lệnh từ plugins sẽ hiển thị khi có plugin đăng ký!`

	b.sendMessage(userID, help)
}

// handlePluginsList - Liệt kê plugins
func (b *BotServer) handlePluginsList(userID string) {
	ctx := context.Background()
	plugins, err := b.registry.ListPlugins(ctx)
	if err != nil {
		b.sendMessage(userID, "❌ Lỗi khi lấy danh sách plugins")
		return
	}

	if len(plugins) == 0 {
		b.sendMessage(userID, "Chưa có plugin nào đăng ký!")
		return
	}

	var msg strings.Builder
	msg.WriteString("🔌 *Danh sách Plugins:*\n\n")
	for _, p := range plugins {
		msg.WriteString(fmt.Sprintf("• %s (%s)\n", p.Name, p.Version))
		msg.WriteString(fmt.Sprintf("  Lệnh: %s\n\n", strings.Join(p.Commands, ", ")))
	}

	b.sendMessage(userID, msg.String())
}

// handleHealth - Health check endpoint
func (b *BotServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
		"time":   time.Now().Format(time.RFC3339),
	})
}

// handleListPlugins - API để xem plugins
func (b *BotServer) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	plugins, err := b.registry.ListPlugins(ctx)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(plugins)
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
