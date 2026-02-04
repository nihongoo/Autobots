package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"autobots/pkg/protocol"
)

type Registry struct {
	client *redis.Client
	ttl    time.Duration
}

// NewRegistry tạo registry mới
func NewRegistry(host, port, password string, ttl int) (*Registry, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", host, port),
		Password: password,
		DB:       0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("không thể kết nối Redis: %w", err)
	}

	return &Registry{
		client: client,
		ttl:    time.Duration(ttl) * time.Second,
	}, nil
}

// Register đăng ký plugin
func (r *Registry) Register(ctx context.Context, info protocol.PluginInfo) error {
	key := fmt.Sprintf("autobots:registry:%s", info.Name)
	
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("không thể marshal plugin info: %w", err)
	}

	// Lưu với TTL, plugin phải renew định kỳ
	return r.client.Set(ctx, key, data, r.ttl).Err()
}

// GetPlugin lấy thông tin plugin
func (r *Registry) GetPlugin(ctx context.Context, name string) (*protocol.PluginInfo, error) {
	key := fmt.Sprintf("autobots:registry:%s", name)
	
	data, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, fmt.Errorf("plugin %s không tồn tại", name)
	}
	if err != nil {
		return nil, err
	}

	var info protocol.PluginInfo
	if err := json.Unmarshal([]byte(data), &info); err != nil {
		return nil, err
	}

	return &info, nil
}

// FindPluginByCommand tìm plugin hỗ trợ command
func (r *Registry) FindPluginByCommand(ctx context.Context, command string) (*protocol.PluginInfo, error) {
	// Lấy tất cả plugins
	keys, err := r.client.Keys(ctx, "autobots:registry:*").Result()
	if err != nil {
		return nil, err
	}

	for _, key := range keys {
		data, err := r.client.Get(ctx, key).Result()
		if err != nil {
			continue
		}

		var info protocol.PluginInfo
		if err := json.Unmarshal([]byte(data), &info); err != nil {
			continue
		}

		// Check nếu plugin hỗ trợ command này
		for _, cmd := range info.Commands {
			if cmd == command {
				return &info, nil
			}
		}
	}

	return nil, fmt.Errorf("không tìm thấy plugin cho command: %s", command)
}

// ListPlugins liệt kê tất cả plugins
func (r *Registry) ListPlugins(ctx context.Context) ([]protocol.PluginInfo, error) {
	keys, err := r.client.Keys(ctx, "autobots:registry:*").Result()
	if err != nil {
		return nil, err
	}

	var plugins []protocol.PluginInfo
	for _, key := range keys {
		data, err := r.client.Get(ctx, key).Result()
		if err != nil {
			continue
		}

		var info protocol.PluginInfo
		if err := json.Unmarshal([]byte(data), &info); err != nil {
			continue
		}
		plugins = append(plugins, info)
	}

	return plugins, nil
}

// Close đóng kết nối
func (r *Registry) Close() error {
	return r.client.Close()
}