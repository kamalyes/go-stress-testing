/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-12-30 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-12-30 12:00:00
 * @FilePath: \go-stress\executor\worker.go
 * @Description: Worker实现
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package executor

import (
	"context"
	"fmt"
	"strings"

	"github.com/kamalyes/go-stress/logger"
	"github.com/kamalyes/go-stress/protocol"
	"github.com/kamalyes/go-stress/statistics"
	"github.com/kamalyes/go-stress/types"
)

// Worker 工作单元
type Worker struct {
	id          uint64
	client      types.Client
	handler     RequestHandler
	collector   *statistics.Collector
	reqCount    uint64
	reqBuilder  *RequestBuilder // 单API模式使用
	apiSelector APISelector     // 多API模式使用
}

// WorkerConfig Worker配置
type WorkerConfig struct {
	ID          uint64
	Client      types.Client
	Handler     RequestHandler
	Collector   *statistics.Collector
	ReqCount    uint64
	ReqBuilder  *RequestBuilder // 单API模式使用（可选）
	APISelector APISelector     // 多API模式使用（可选）
}

// NewWorker 创建Worker
func NewWorker(cfg WorkerConfig) *Worker {
	return &Worker{
		id:          cfg.ID,
		client:      cfg.Client,
		handler:     cfg.Handler,
		collector:   cfg.Collector,
		reqCount:    cfg.ReqCount,
		reqBuilder:  cfg.ReqBuilder,
		apiSelector: cfg.APISelector,
	}
}

// Run 运行Worker
func (w *Worker) Run(ctx context.Context) error {
	// 建立连接
	if err := w.client.Connect(ctx); err != nil {
		logger.Default.Errorf("❌ Worker %d: 连接失败: %v", w.id, err)
		return err
	}
	defer w.client.Close()

	// 执行请求
	for i := uint64(0); i < w.reqCount; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		w.executeRequest(ctx)
	}

	return nil
}

// executeRequest 执行单次请求
func (w *Worker) executeRequest(ctx context.Context) {
	// 构建请求
	var req *types.Request
	var apiCfg *APIRequestConfig

	if w.apiSelector != nil {
		// 多API模式：从选择器获取下一个API
		apiCfg = w.apiSelector.Next()
		if apiCfg == nil {
			logger.Default.Error("API选择器返回空配置")
			return
		}

		// 如果有依赖关系，需要替换提取的变量
		if w.apiSelector.HasDependencies() {
			resolver := w.apiSelector.GetDependencyResolver()
			if resolver != nil {
				apiCfg = w.replaceExtractedVars(apiCfg, resolver)
			}
		}

		req = BuildRequest(apiCfg)
	} else if w.reqBuilder != nil {
		// 单API模式：使用请求构建器
		req = w.reqBuilder.Build()
	} else {
		logger.Default.Error("Worker既没有API选择器也没有请求构建器")
		return
	}

	// 执行请求（通过中间件链）
	resp, err := w.handler(ctx, req)

	// 如果有API级别的验证配置，执行验证
	if apiCfg != nil && len(apiCfg.Verify) > 0 && resp != nil && err == nil {
		err = w.executeVerifications(apiCfg, resp)
	}

	// 如果有提取器配置，提取数据
	if apiCfg != nil && len(apiCfg.Extractors) > 0 && resp != nil {
		w.extractAndStoreVars(apiCfg, resp)
	}

	// 记录结果
	result := BuildRequestResult(resp, err)
	w.collector.Collect(result)
}

// replaceExtractedVars 替换API配置中的提取变量
func (w *Worker) replaceExtractedVars(apiCfg *APIRequestConfig, resolver *DependencyResolver) *APIRequestConfig {
	extractedVars := resolver.GetAllExtractedVars()
	if len(extractedVars) == 0 {
		return apiCfg
	}

	// 复制配置避免修改原始数据
	newCfg := &APIRequestConfig{
		Name:       apiCfg.Name,
		URL:        replaceVars(apiCfg.URL, extractedVars),
		Method:     apiCfg.Method,
		Headers:    make(map[string]string),
		Body:       replaceVars(apiCfg.Body, extractedVars),
		Verify:     apiCfg.Verify,
		Extractors: apiCfg.Extractors,
	}

	// 替换headers中的变量
	for k, v := range apiCfg.Headers {
		newCfg.Headers[k] = replaceVars(v, extractedVars)
	}

	return newCfg
}

// replaceVars 替换字符串中的变量占位符 {{.apiName.varName}}
func replaceVars(text string, vars map[string]string) string {
	result := text
	for key, value := range vars {
		placeholder := "{{." + key + "}}"
		result = strings.ReplaceAll(result, placeholder, value)
	}
	return result
}

// extractAndStoreVars 提取响应数据并存储
func (w *Worker) extractAndStoreVars(apiCfg *APIRequestConfig, resp *types.Response) {
	// 获取依赖解析器
	if !w.apiSelector.HasDependencies() {
		return
	}

	resolver := w.apiSelector.GetDependencyResolver()
	if resolver == nil {
		return
	}

	// 构建默认值映射
	defaultValues := make(map[string]string)
	for _, extCfg := range apiCfg.Extractors {
		if extCfg.Default != "" {
			defaultValues[extCfg.Name] = extCfg.Default
		}
	}

	// 创建提取器管理器
	manager, err := NewExtractorManager(apiCfg.Extractors)
	if err != nil {
		logger.Default.Error("创建提取器失败 [%s]: %v", apiCfg.Name, err)
		return
	}

	// 提取所有变量
	extractedVars := manager.ExtractAll(resp, defaultValues)

	// 存储到依赖解析器
	if len(extractedVars) > 0 {
		resolver.StoreExtractedVars(apiCfg.Name, extractedVars)
		logger.Default.Info("📦 API [%s] 提取了 %d 个变量", apiCfg.Name, len(extractedVars))
	}
}

// executeVerifications 执行API级别的验证
func (w *Worker) executeVerifications(apiCfg *APIRequestConfig, resp *types.Response) error {
	for _, verifyCfg := range apiCfg.Verify {
		// 直接创建HTTP验证器
		httpVerifier := protocol.NewHTTPVerifier(&verifyCfg)

		// 执行验证
		isValid, verifyErr := httpVerifier.Verify(resp)
		if !isValid {
			if verifyErr != nil {
				return fmt.Errorf("响应验证失败: %w", verifyErr)
			}
			return fmt.Errorf("响应验证失败")
		}
	}
	return nil
}
