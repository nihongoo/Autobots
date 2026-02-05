package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"autobots/pkg/protocol"
	"autobots/pkg/queue"
	"autobots/pkg/registry"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Ironhide struct {
	name       string
	queue      *queue.Queue
	registry   *registry.Registry
	mongodb    *mongo.Client
	collection *mongo.Collection
}

type Quote struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	Text      string             `bson:"text"`
	Author    string             `bson:"author"`
	Category  string             `bson:"category,omitempty"`
	AddedBy   string             `bson:"added_by"`
	CreatedAt time.Time          `bson:"created_at"`
}

func main() {

	redisHost := getEnv("REDIS_HOST", "localhost")
	redisPort := getEnv("REDIS_PORT", "6379")
	redisPassword := getEnv("REDIS_PASSWORD", "")
	mongoURI := os.Getenv("MONGODB_URI")

	if mongoURI == "" {
		log.Fatal("Thiếu MONGODB_URI trong biến môi trường")
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

	// Khởi tạo MongoDB
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		log.Fatalf("Không thể kết nối MongoDB: %v", err)
	}
	defer client.Disconnect(context.Background())

	// Ping MongoDB
	if err := client.Ping(ctx, nil); err != nil {
		log.Fatalf("MongoDB ping failed: %v", err)
	}

	log.Println("✅ Đã kết nối MongoDB!")

	plugin := &Ironhide{
		name:       "Ironhide",
		queue:      q,
		registry:   reg,
		mongodb:    client,
		collection: client.Database("autobots").Collection("quotes"),
	}

	// Tạo text index cho search
	plugin.createIndexes()

	// Đăng ký plugin
	if err := plugin.register(); err != nil {
		log.Fatalf("Không thể đăng ký plugin: %v", err)
	}

	// Heartbeat
	go plugin.heartbeat()

	log.Printf("✅ Đã đăng ký plugin: %s", plugin.name)
	// Lắng nghe requests
	log.Printf("🤖 %s - Quote guardian is ready!", plugin.name)
	plugin.listen()
}

func (p *Ironhide) createIndexes() {
	ctx := context.Background()

	// Text index cho search
	indexModel := mongo.IndexModel{
		Keys: bson.D{
			{Key: "text", Value: "text"},
			{Key: "author", Value: "text"},
		},
	}

	if _, err := p.collection.Indexes().CreateOne(ctx, indexModel); err != nil {
		log.Printf("⚠️ Không tạo được index: %v", err)
	}
}

func (p *Ironhide) register() error {
	ctx := context.Background()
	info := protocol.PluginInfo{
		Name:        "Ironhide",
		Description: "Quote storage guardian - Save, search, and retrieve quotes",
		Commands:    []string{"quote-add", "quote-find", "quote-random", "quote-list", "quote-delete"},
		Version:     "v1.0.0",
		HealthURL:   "http://localhost:8082/health",
		QueueName:   "autobots:ironhide",
	}

	if err := p.registry.Register(ctx, info); err != nil {
		return fmt.Errorf("lỗi đăng ký: %w", err)
	}
	return nil
}

func (p *Ironhide) heartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if err := p.register(); err != nil {
			log.Printf("⚠️ Heartbeat failed: %v", err)
		}
	}
}

func (p *Ironhide) listen() {
	ctx := context.Background()
	queueName := "autobots:ironhide"

	log.Printf("👂 Đang lắng nghe queue: %s", queueName)

	err := p.queue.Subscribe(ctx, queueName, func(data []byte) error {
		var request protocol.PluginRequest
		if err := json.Unmarshal(data, &request); err != nil {
			return fmt.Errorf("lỗi parse request: %w", err)
		}

		log.Printf("📨 Nhận request: %s - Command: %s", request.RequestID, request.Command)

		// Xử lý command
		response := p.handleCommand(request)

		// Gửi response về bot
		if err := p.queue.Publish(ctx, "autobots:responses", response); err != nil {
			log.Printf("❌ Lỗi gửi response: %v", err)
		}

		return nil
	})

	if err != nil {
		log.Fatalf("Lỗi khi subscribe: %v", err)
	}
}

func (p *Ironhide) handleCommand(req protocol.PluginRequest) protocol.PluginResponse {
	response := protocol.PluginResponse{
		RequestID:   req.RequestID,
		CompletedAt: time.Now(),
		Metadata: map[string]interface{}{
			"user_id": req.UserID,
		},
	}

	ctx := context.Background()

	switch req.Command {
	case "quote-add":
		response = p.addQuote(ctx, req)
	case "quote-find":
		response = p.findQuote(ctx, req)
	case "quote-random":
		response = p.randomQuote(ctx, req)
	case "quote-list":
		response = p.listQuotes(ctx, req)
	case "quote-delete":
		response = p.deleteQuote(ctx, req)
	default:
		response.Status = "error"
		response.Error = fmt.Sprintf("❌ Command không hợp lệ: %s", req.Command)
	}

	return response
}

// Thêm quote mới
func (p *Ironhide) addQuote(ctx context.Context, req protocol.PluginRequest) protocol.PluginResponse {
	response := protocol.PluginResponse{
		RequestID:   req.RequestID,
		CompletedAt: time.Now(),
		Metadata:    map[string]interface{}{"user_id": req.UserID},
	}

	if len(req.Args) < 1 {
		response.Status = "error"
		response.Error = "Cách dùng: /quote-add \"quote text\" - Author [category]\nVí dụ: /quote-add \"Success is not final\" - Winston Churchill motivational"
		return response
	}

	// Parse quote text và author
	// fullText := strings.Join(req.Args, " ")

	// fullText = strings.ReplaceAll(fullText, "|", "\n")
	fullText := req.RawInput

	var quoteText, author, category string

	// Tìm author (sau dấu -)
	if idx := strings.Index(fullText, " - "); idx != -1 {
		quoteText = strings.Trim(fullText[:idx], "\"")
		remaining := strings.TrimSpace(fullText[idx+3:])

		// Tách author và category
		parts := strings.Fields(remaining)
		if len(parts) > 0 {
			// Author là phần đầu, category là phần sau (nếu có)
			authorParts := []string{}
			categoryParts := []string{}
			foundCategory := false

			for _, part := range parts {
				if strings.Contains(strings.ToLower(part), "motivational") ||
					strings.Contains(strings.ToLower(part), "love") ||
					strings.Contains(strings.ToLower(part), "success") ||
					strings.Contains(strings.ToLower(part), "life") {
					foundCategory = true
				}

				if foundCategory {
					categoryParts = append(categoryParts, part)
				} else {
					authorParts = append(authorParts, part)
				}
			}

			author = strings.Join(authorParts, " ")
			category = strings.Join(categoryParts, " ")
		}
	} else {
		quoteText = strings.Trim(fullText, "\"")
		author = "Unknown"
	}

	quote := Quote{
		Text:      quoteText,
		Author:    author,
		Category:  category,
		AddedBy:   req.UserID,
		CreatedAt: time.Now(),
	}

	result, err := p.collection.InsertOne(ctx, quote)
	if err != nil {
		response.Status = "error"
		response.Error = fmt.Sprintf("Lỗi khi lưu quote: %v", err)
		return response
	}

	response.Status = "success"
	response.Result = fmt.Sprintf("✅ Đã lưu quote!\n\n\"%s\"\n- %s", quoteText, author)
	if category != "" {
		response.Result += fmt.Sprintf("\n📁 Category: %s", category)
	}
	response.Result += fmt.Sprintf("\n\n🆔 ID: %s", result.InsertedID.(primitive.ObjectID).Hex())

	return response
}

// Tìm quote
func (p *Ironhide) findQuote(ctx context.Context, req protocol.PluginRequest) protocol.PluginResponse {
	response := protocol.PluginResponse{
		RequestID:   req.RequestID,
		CompletedAt: time.Now(),
		Metadata:    map[string]interface{}{"user_id": req.UserID},
	}

	if len(req.Args) < 1 {
		response.Status = "error"
		response.Error = "Cách dùng: /quote-find <từ khóa>\nVí dụ: /quote-find success"
		return response
	}

	keyword := strings.Join(req.Args, " ")

	// Search với text index
	filter := bson.M{
		"$text": bson.M{
			"$search": keyword,
		},
	}

	cursor, err := p.collection.Find(ctx, filter, options.Find().SetLimit(5))
	if err != nil {
		response.Status = "error"
		response.Error = fmt.Sprintf("Lỗi khi tìm kiếm: %v", err)
		return response
	}
	defer cursor.Close(ctx)

	var quotes []Quote
	if err := cursor.All(ctx, &quotes); err != nil {
		response.Status = "error"
		response.Error = fmt.Sprintf("Lỗi khi đọc kết quả: %v", err)
		return response
	}

	if len(quotes) == 0 {
		response.Status = "success"
		response.Result = fmt.Sprintf("❌ Không tìm thấy quote nào với từ khóa: \"%s\"", keyword)
		return response
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("🔍 Tìm thấy %d quote(s) với từ khóa \"%s\":\n\n", len(quotes), keyword))

	for i, q := range quotes {
		result.WriteString(fmt.Sprintf("%d. \"%s\"\n   - %s", i+1, q.Text, q.Author))
		if q.Category != "" {
			result.WriteString(fmt.Sprintf(" [%s]", q.Category))
		}
		result.WriteString(fmt.Sprintf("\n   🆔 %s\n\n", q.ID.Hex()))
	}

	response.Status = "success"
	response.Result = result.String()
	return response
}

// Random quote
func (p *Ironhide) randomQuote(ctx context.Context, req protocol.PluginRequest) protocol.PluginResponse {
	response := protocol.PluginResponse{
		RequestID:   req.RequestID,
		CompletedAt: time.Now(),
		Metadata:    map[string]interface{}{"user_id": req.UserID},
	}

	// Aggregation pipeline để lấy random
	pipeline := mongo.Pipeline{
		{{Key: "$sample", Value: bson.D{{Key: "size", Value: 1}}}},
	}

	cursor, err := p.collection.Aggregate(ctx, pipeline)
	if err != nil {
		response.Status = "error"
		response.Error = fmt.Sprintf("Lỗi khi lấy quote: %v", err)
		return response
	}
	defer cursor.Close(ctx)

	var quotes []Quote
	if err := cursor.All(ctx, &quotes); err != nil {
		response.Status = "error"
		response.Error = fmt.Sprintf("Lỗi khi đọc quote: %v", err)
		return response
	}

	if len(quotes) == 0 {
		response.Status = "success"
		response.Result = "❌ Chưa có quote nào trong database. Thêm quote bằng /quote-add"
		return response
	}

	q := quotes[0]
	response.Status = "success"
	response.Result = fmt.Sprintf("🎲 Random Quote:\n\n\"%s\"\n- %s", q.Text, q.Author)
	if q.Category != "" {
		response.Result += fmt.Sprintf("\n📁 %s", q.Category)
	}

	return response
}

// List tất cả quotes
func (p *Ironhide) listQuotes(ctx context.Context, req protocol.PluginRequest) protocol.PluginResponse {
	response := protocol.PluginResponse{
		RequestID:   req.RequestID,
		CompletedAt: time.Now(),
		Metadata:    map[string]interface{}{"user_id": req.UserID},
	}

	// Đếm tổng số
	count, err := p.collection.CountDocuments(ctx, bson.M{})
	if err != nil {
		response.Status = "error"
		response.Error = fmt.Sprintf("Lỗi: %v", err)
		return response
	}

	if count == 0 {
		response.Status = "success"
		response.Result = "❌ Chưa có quote nào. Thêm quote bằng /quote-add"
		return response
	}

	// Lấy 10 quotes mới nhất
	cursor, err := p.collection.Find(
		ctx,
		bson.M{},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(10),
	)
	if err != nil {
		response.Status = "error"
		response.Error = fmt.Sprintf("Lỗi: %v", err)
		return response
	}
	defer cursor.Close(ctx)

	var quotes []Quote
	if err := cursor.All(ctx, &quotes); err != nil {
		response.Status = "error"
		response.Error = fmt.Sprintf("Lỗi: %v", err)
		return response
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("📚 Tổng số: %d quotes\n", count))
	result.WriteString("📝 10 quotes mới nhất:\n\n")

	for i, q := range quotes {
		result.WriteString(fmt.Sprintf("%d. \"%s\"\n   - %s", i+1, q.Text, q.Author))
		if q.Category != "" {
			result.WriteString(fmt.Sprintf(" [%s]", q.Category))
		}
		result.WriteString("\n\n")
	}

	response.Status = "success"
	response.Result = result.String()
	return response
}

// Xóa quote
func (p *Ironhide) deleteQuote(ctx context.Context, req protocol.PluginRequest) protocol.PluginResponse {
	response := protocol.PluginResponse{
		RequestID:   req.RequestID,
		CompletedAt: time.Now(),
		Metadata:    map[string]interface{}{"user_id": req.UserID},
	}

	if len(req.Args) < 1 {
		response.Status = "error"
		response.Error = "Cách dùng: /quote-delete <ID>\nVí dụ: /quote-delete 507f1f77bcf86cd799439011"
		return response
	}

	idStr := req.Args[0]
	objectID, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		response.Status = "error"
		response.Error = "❌ ID không hợp lệ"
		return response
	}

	result, err := p.collection.DeleteOne(ctx, bson.M{"_id": objectID})
	if err != nil {
		response.Status = "error"
		response.Error = fmt.Sprintf("Lỗi khi xóa: %v", err)
		return response
	}

	if result.DeletedCount == 0 {
		response.Status = "error"
		response.Error = "❌ Không tìm thấy quote với ID này"
		return response
	}

	response.Status = "success"
	response.Result = "✅ Đã xóa quote!"
	return response
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
