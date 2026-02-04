package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"autobots/pkg/protocol"
	"autobots/pkg/queue"
	"autobots/pkg/registry"
)

type OptimusPrime struct {
	name     string
	queue    *queue.Queue
	registry *registry.Registry
}

func main() {
	// Load config
	// if err := godotenv.Load("../../../config/.env"); err != nil {
	// 	log.Println("Không tìm thấy file .env, dùng biến môi trường")
	// }

	redisHost := getEnv("REDIS_HOST", "localhost")
	redisPort := getEnv("REDIS_PORT", "6379")
	redisPassword := getEnv("REDIS_PASSWORD", "")

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

	plugin := &OptimusPrime{
		name:     "OptimusPrime",
		queue:    q,
		registry: reg,
	}

	// Đăng ký plugin
	if err := plugin.register(); err != nil {
		log.Fatalf("Không thể đăng ký plugin: %v", err)
	}

	// Heartbeat - renew registration mỗi 30s
	go plugin.heartbeat()

	// Lắng nghe requests
	log.Printf("🤖 %s is ready to roll out!", plugin.name)
	plugin.listen()
}

func (p *OptimusPrime) register() error {
	ctx := context.Background()
	info := protocol.PluginInfo{
		Name:        "OptimusPrime",
		Description: "Image processing leader - Convert, resize, filter images",
		Commands:    []string{"convert", "resize", "grayscale"},
		Version:     "v1.0.0",
		HealthURL:   "http://localhost:8081/health",
		QueueName:   "autobots:optimusprime",
	}

	if err := p.registry.Register(ctx, info); err != nil {
		return fmt.Errorf("lỗi đăng ký: %w", err)
	}

	log.Printf("✅ Đã đăng ký plugin: %s", info.Name)
	return nil
}

func (p *OptimusPrime) heartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if err := p.register(); err != nil {
			log.Printf("⚠️ Heartbeat failed: %v", err)
		} else {
			log.Println("💓 Heartbeat sent")
		}
	}
}

func (p *OptimusPrime) listen() {
	ctx := context.Background()
	queueName := "autobots:optimusprime"

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

func (p *OptimusPrime) handleCommand(req protocol.PluginRequest) protocol.PluginResponse {
	response := protocol.PluginResponse{
		RequestID:   req.RequestID,
		CompletedAt: time.Now(),
		Metadata: map[string]interface{}{
			"user_id": req.UserID,
		},
	}

	switch req.Command {
	case "convert":
		if len(req.Args) < 2 {
			response.Status = "error"
			response.Error = "Thiếu tham số! Dùng: /convert <input> <format>"
			return response
		}
		
		inputFile := req.Args[0]
		outputFormat := req.Args[1]
		
		response.Status = "success"
		response.Result = fmt.Sprintf("✅ Đã convert %s sang %s!\n(Demo - chưa xử lý thật)", 
			inputFile, outputFormat)

	case "resize":
		if len(req.Args) < 3 {
			response.Status = "error"
			response.Error = "Thiếu tham số! Dùng: /resize <file> <width> <height>"
			return response
		}
		
		fileName := req.Args[0]
		width := req.Args[1]
		height := req.Args[2]
		
		response.Status = "success"
		response.Result = fmt.Sprintf("✅ Đã resize %s thành %sx%s!\n(Demo - chưa xử lý thật)", 
			fileName, width, height)

	case "grayscale":
		if len(req.Args) < 1 {
			response.Status = "error"
			response.Error = "Thiếu tham số! Dùng: /grayscale <file>"
			return response
		}
		
		fileName := req.Args[0]
		
		response.Status = "success"
		response.Result = fmt.Sprintf("✅ Đã chuyển %s sang grayscale!\n(Demo - chưa xử lý thật)", 
			fileName)

	default:
		response.Status = "error"
		response.Error = fmt.Sprintf("❌ Command không hợp lệ: %s", req.Command)
	}

	return response
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}