package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type Queue struct {
	client *redis.Client
}

// NewQueue tạo queue mới
func NewQueue(host, port, password string) (*Queue, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", host, port),
		Password: password,
		DB:       0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Test connection
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("không thể kết nối Redis: %w", err)
	}

	return &Queue{client: client}, nil
}

// Publish gửi message vào queue
func (q *Queue) Publish(ctx context.Context, queueName string, message interface{}) error {
	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("không thể marshal message: %w", err)
	}

	return q.client.LPush(ctx, queueName, data).Err()
}

// Subscribe lắng nghe message từ queue (blocking)
func (q *Queue) Subscribe(ctx context.Context, queueName string, handler func([]byte) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// BRPOP: blocking right pop (chờ đến khi có message)
			result, err := q.client.BRPop(ctx, 0, queueName).Result()
			if err != nil {
				if err == redis.Nil {
					continue
				}
				return fmt.Errorf("lỗi khi đọc queue: %w", err)
			}

			if len(result) < 2 {
				continue
			}

			// result[0] là tên queue, result[1] là data
			data := []byte(result[1])
			
			// Xử lý message
			if err := handler(data); err != nil {
				// Log lỗi nhưng vẫn tiếp tục
				fmt.Printf("Lỗi khi xử lý message: %v\n", err)
			}
		}
	}
}

// Close đóng kết nối
func (q *Queue) Close() error {
	return q.client.Close()
}