/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-12-30 09:59:13
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-12-30 17:18:08
 * @FilePath: \go-stress\protocol\http_verify.go
 * @Description: HTTP验证器
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package protocol

import (
	"encoding/json"
	"fmt"

	"github.com/kamalyes/go-stress/config"
	"github.com/kamalyes/go-toolbox/pkg/stringx"
	"github.com/oliveagle/jsonpath"
)

// HTTPVerifier HTTP验证器
type HTTPVerifier struct {
	config *config.VerifyConfig
}

// NewHTTPVerifier 创建HTTP验证器
func NewHTTPVerifier(cfg *config.VerifyConfig) *HTTPVerifier {
	if cfg == nil {
		cfg = &config.VerifyConfig{
			Type:   VerifyTypeStatusCode,
			Expect: 200,
		}
	}
	return &HTTPVerifier{config: cfg}
}

// Verify 验证HTTP响应
func (v *HTTPVerifier) Verify(resp *Response) (bool, error) {
	if resp.Error != nil {
		return false, resp.Error
	}

	// 初始化验证结果列表
	if resp.Verifications == nil {
		resp.Verifications = make([]VerificationResult, 0)
	}

	switch v.config.Type {
	case VerifyTypeStatusCode:
		return v.verifyStatusCode(resp)
	case VerifyTypeJSONPath:
		return v.verifyJSONPath(resp)
	case VerifyTypeContains:
		return v.verifyContains(resp)
	default:
		return true, nil
	}
}

// verifyStatusCode 验证状态码
func (v *HTTPVerifier) verifyStatusCode(resp *Response) (bool, error) {
	expectedCode := v.config.Expect
	if expectedCode == 0 {
		expectedCode = 200
	}

	success := resp.StatusCode == expectedCode
	result := VerificationResult{
		Type:    v.config.Type,
		Success: success,
		Expect:  fmt.Sprintf("%v", expectedCode),
		Actual:  fmt.Sprintf("%d", resp.StatusCode),
	}

	if !success {
		result.Message = fmt.Sprintf("状态码不匹配: 期望 %d, 实际 %d", expectedCode, resp.StatusCode)
	} else {
		result.Message = "状态码验证通过"
	}

	resp.Verifications = append(resp.Verifications, result)

	if !success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// verifyJSONPath 验证JSON路径
func (v *HTTPVerifier) verifyJSONPath(resp *Response) (bool, error) {
	// 首先检查状态码
	if ok, _ := v.verifyStatusCode(resp); !ok {
		return false, fmt.Errorf("状态码验证失败")
	}

	var data interface{}
	if err := json.Unmarshal(resp.Body, &data); err != nil {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("解析JSON失败: %v", err),
			Expect:  v.config.JSONPath,
			Actual:  "解析失败",
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("解析JSON失败: %s", result.Message)
	}

	// 使用 jsonpath 库查询
	value, err := jsonpath.JsonPathLookup(data, v.config.JSONPath)
	if err != nil {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("JSON路径查询失败: %v", err),
			Expect:  v.config.JSONPath,
			Actual:  "查询失败",
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("JSON路径查询失败: %s", result.Message)
	}

	result := VerificationResult{
		Type:    v.config.Type,
		Success: true,
		Message: "JSON路径验证通过",
		Expect:  v.config.JSONPath,
		Actual:  fmt.Sprintf("%v", value),
	}
	resp.Verifications = append(resp.Verifications, result)

	return true, nil
}

// verifyContains 验证包含字符串
func (v *HTTPVerifier) verifyContains(resp *Response) (bool, error) {
	// 首先检查状态码
	if ok, _ := v.verifyStatusCode(resp); !ok {
		return false, fmt.Errorf("状态码验证失败")
	}

	bodyStr := string(resp.Body)
	containsStr, ok := v.config.Expect.(string)
	if !ok {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: "contains 验证需要字符串类型的 expect 值",
			Expect:  fmt.Sprintf("%v", v.config.Expect),
			Actual:  "类型错误",
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("类型错误: %s", result.Message)
	}

	success := stringx.Contains(bodyStr, containsStr)
	result := VerificationResult{
		Type:    v.config.Type,
		Success: success,
		Expect:  containsStr,
		Actual:  bodyStr,
	}

	if !success {
		result.Message = fmt.Sprintf("响应不包含期望的字符串: %s", containsStr)
	} else {
		result.Message = "包含字符串验证通过"
	}

	resp.Verifications = append(resp.Verifications, result)

	if !success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}
