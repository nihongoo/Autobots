package protocol

import "time"

// PluginRequest - Message từ bot gửi đến plugin
type PluginRequest struct {
	RequestID   string                 `json:"request_id"`
	Command     string                 `json:"command"`      // Tên lệnh: convert, resize, ...
	Args        []string               `json:"args"`         // Tham số
	RawInput    string                 `json:"raw_input"`    // ← THÊM DÒNG NÀY - Input nguyên bản
	UserID      string                 `json:"user_id"`      // Facebook User ID
	Files       []FileAttachment       `json:"files"`        // File đính kèm
	Metadata    map[string]interface{} `json:"metadata"`     // Thông tin thêm
	CreatedAt   time.Time              `json:"created_at"`
}

// PluginResponse - Message plugin trả về bot
type PluginResponse struct {
	RequestID   string                 `json:"request_id"`
	Status      string                 `json:"status"`       // success, error, processing
	Result      string                 `json:"result"`       // Text response
	Files       []FileAttachment       `json:"files"`        // File kết quả
	Error       string                 `json:"error"`        // Thông báo lỗi (nếu có)
	Metadata    map[string]interface{} `json:"metadata"`
	CompletedAt time.Time              `json:"completed_at"`
}

// FileAttachment - Thông tin file
type FileAttachment struct {
	URL      string `json:"url"`       // URL để download
	Type     string `json:"type"`      // image, video, document
	Name     string `json:"name"`      // Tên file
	Size     int64  `json:"size"`      // Kích thước (bytes)
	MimeType string `json:"mime_type"` // image/jpeg, video/mp4, ...
}

// PluginInfo - Thông tin đăng ký plugin
type PluginInfo struct {
	Name        string   `json:"name"`         // optimusprime, bumblebee, ...
	Description string   `json:"description"`  // Mô tả plugin
	Commands    []string `json:"commands"`     // Danh sách lệnh hỗ trợ
	Version     string   `json:"version"`      // v1.0.0
	HealthURL   string   `json:"health_url"`   // http://plugin:8080/health
	QueueName   string   `json:"queue_name"`   // autobots:optimusprime
}

// HealthStatus - Trạng thái health check
type HealthStatus struct {
	Status    string    `json:"status"`     // healthy, unhealthy
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message"`
}