/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-12-30 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-12-30 13:10:00
 * @FilePath: \go-stress\statistics\realtime_server.go
 * @Description: 实时报告服务器
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package statistics

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/kamalyes/go-stress/logger"
)

// RealtimeServer 实时报告服务器
type RealtimeServer struct {
	collector   *Collector
	server      *http.Server
	clients     map[chan []byte]bool
	mu          sync.RWMutex
	startTime   time.Time
	endTime     time.Time
	isCompleted bool
	port        int
	ctx         context.Context
	cancel      context.CancelFunc
}

// RealtimeData 实时数据
type RealtimeData struct {
	Timestamp       int64   `json:"timestamp"`
	TotalRequests   uint64  `json:"total_requests"`
	SuccessRequests uint64  `json:"success_requests"`
	FailedRequests  uint64  `json:"failed_requests"`
	SuccessRate     float64 `json:"success_rate"`
	QPS             float64 `json:"qps"`
	AvgDuration     int64   `json:"avg_duration_ms"`
	MinDuration     int64   `json:"min_duration_ms"`
	MaxDuration     int64   `json:"max_duration_ms"`
	Elapsed         int64   `json:"elapsed_seconds"`

	// 错误统计
	Errors map[string]uint64 `json:"errors,omitempty"`

	// 状态码统计
	StatusCodes map[int]uint64 `json:"status_codes,omitempty"`

	// 最近的响应时间点（用于实时图表）
	RecentDurations []int64 `json:"recent_durations,omitempty"`
}

// NewRealtimeServer 创建实时报告服务器
func NewRealtimeServer(collector *Collector, port int) *RealtimeServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &RealtimeServer{
		collector: collector,
		clients:   make(map[chan []byte]bool),
		startTime: time.Now(),
		port:      port,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Start 启动服务器
func (s *RealtimeServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/stream", s.handleStream)
	mux.HandleFunc("/api/data", s.handleData)
	mux.HandleFunc("/api/details", s.handleDetails)

	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	go func() {
		logger.Default.Info("🌐 实时报告服务器启动: http://localhost:%d", s.port)
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Default.Errorf("实时报告服务器错误: %v", err)
		}
	}()

	// 启动数据广播
	go s.broadcastLoop()

	return nil
}

// MarkCompleted 标记测试完成（固定结束时间，避免 QPS 继续变化）
func (s *RealtimeServer) MarkCompleted() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isCompleted {
		s.endTime = time.Now()
		s.isCompleted = true
		logger.Default.Debug("实时服务器已标记为完成状态")
	}
}

// Stop 停止服务器
func (s *RealtimeServer) Stop() error {
	// 取消context，停止broadcastLoop
	if s.cancel != nil {
		s.cancel()
	}

	// 关闭所有客户端连接
	s.mu.Lock()
	for clientChan := range s.clients {
		close(clientChan)
	}
	s.clients = make(map[chan []byte]bool)
	s.mu.Unlock()

	// 关闭HTTP服务器
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.server.Shutdown(ctx)
	}
	return nil
}

// handleIndex 处理首页
func (s *RealtimeServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// 使用统一模板，设置为实时模式
	data := &HTMLReportData{
		IsRealtime: true,
	}

	tmpl, err := template.New("realtime").Parse(unifiedTemplate)
	if err != nil {
		http.Error(w, "模板解析失败", http.StatusInternalServerError)
		return
	}

	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "模板执行失败", http.StatusInternalServerError)
	}
}

// handleStream 处理SSE流
func (s *RealtimeServer) handleStream(w http.ResponseWriter, r *http.Request) {
	// 设置SSE响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// 创建客户端通道
	clientChan := make(chan []byte, 10)
	s.mu.Lock()
	s.clients[clientChan] = true
	s.mu.Unlock()

	// 客户端断开时清理
	defer func() {
		s.mu.Lock()
		delete(s.clients, clientChan)
		s.mu.Unlock()
		close(clientChan)
	}()

	// 发送初始数据
	data := s.collectData()
	jsonData, _ := json.Marshal(data)
	fmt.Fprintf(w, "data: %s\n\n", jsonData)
	w.(http.Flusher).Flush()

	// 持续推送数据
	for {
		select {
		case msg, ok := <-clientChan:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// handleData 处理数据API请求
func (s *RealtimeServer) handleData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	data := s.collectData()
	json.NewEncoder(w).Encode(data)
}

// collectData 收集当前数据
func (s *RealtimeServer) collectData() *RealtimeData {
	snapshot := s.collector.GetSnapshot()

	// 如果已完成，使用固定的总时间；否则使用当前经过的时间
	var elapsed float64
	s.mu.RLock()
	if s.isCompleted {
		elapsed = s.endTime.Sub(s.startTime).Seconds()
	} else {
		elapsed = time.Since(s.startTime).Seconds()
	}
	s.mu.RUnlock()

	data := &RealtimeData{
		Timestamp:       time.Now().Unix(),
		TotalRequests:   snapshot.TotalRequests,
		SuccessRequests: snapshot.SuccessRequests,
		FailedRequests:  snapshot.FailedRequests,
		AvgDuration:     snapshot.AvgDuration.Milliseconds(),
		MinDuration:     snapshot.MinDuration.Milliseconds(),
		MaxDuration:     snapshot.MaxDuration.Milliseconds(),
		Elapsed:         int64(elapsed),
	}

	if snapshot.TotalRequests > 0 && elapsed > 0 {
		data.SuccessRate = float64(snapshot.SuccessRequests) / float64(snapshot.TotalRequests) * 100
		data.QPS = float64(snapshot.TotalRequests) / elapsed
	}

	// 获取错误和状态码统计
	s.collector.mu.Lock()
	data.Errors = make(map[string]uint64)
	for k, v := range s.collector.errors {
		data.Errors[k] = v
	}
	data.StatusCodes = make(map[int]uint64)
	for k, v := range s.collector.statusCodes {
		data.StatusCodes[k] = v
	}

	// 获取最近20个响应时间用于实时图表
	durationsLen := len(s.collector.durations)
	if durationsLen > 0 {
		start := 0
		if durationsLen > 20 {
			start = durationsLen - 20
		}
		data.RecentDurations = make([]int64, 0, 20)
		for i := start; i < durationsLen; i++ {
			data.RecentDurations = append(data.RecentDurations, s.collector.durations[i].Milliseconds())
		}
	}
	s.collector.mu.Unlock()

	return data
}

// handleDetails 处理请求明细API
func (s *RealtimeServer) handleDetails(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// 解析查询参数
	query := r.URL.Query()
	offset := 0
	limit := 100
	onlyErrors := query.Get("errors") == "true"

	if o := query.Get("offset"); o != "" {
		fmt.Sscanf(o, "%d", &offset)
	}
	if l := query.Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	// 限制每次最多返回1000条
	if limit > 1000 {
		limit = 1000
	}

	details := s.collector.GetRequestDetails(offset, limit, onlyErrors)
	total := s.collector.GetRequestDetailsCount(onlyErrors)

	response := map[string]interface{}{
		"total":   total,
		"offset":  offset,
		"limit":   limit,
		"details": details,
	}

	json.NewEncoder(w).Encode(response)
}

// broadcastLoop 广播循环
func (s *RealtimeServer) broadcastLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			// 收到退出信号
			return
		case <-ticker.C:
			s.mu.RLock()
			if len(s.clients) == 0 {
				s.mu.RUnlock()
				continue
			}
			s.mu.RUnlock()

			data := s.collectData()
			jsonData, err := json.Marshal(data)
			if err != nil {
				continue
			}

			s.mu.RLock()
			for clientChan := range s.clients {
				select {
				case clientChan <- jsonData:
				default:
					// 通道已满，跳过
				}
			}
			s.mu.RUnlock()
		}
	}
}
