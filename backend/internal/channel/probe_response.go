package channel

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	testStreamLimit = 4 << 20
	testEventLimit  = 1 << 20
)

// A 200 only confirms the stream opened, not that the model ran successfully.
func validateTestResponse(response *http.Response, format Format) error {
	if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		body, err := io.ReadAll(io.LimitReader(response.Body, testBodyLimit+1))
		if err != nil {
			return fmt.Errorf("读取测试响应失败: %w", err)
		}
		if len(body) > testBodyLimit {
			return errors.New("测试响应超过大小限制")
		}
		var value map[string]json.RawMessage
		if json.Unmarshal(body, &value) != nil || len(value) == 0 {
			return errors.New("测试响应不是有效 JSON")
		}
		_, err = testEvent(format, body)
		return err
	}
	limited := &io.LimitedReader{R: response.Body, N: testStreamLimit + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), testEventLimit+1)
	var data []string
	eventBytes := 0
	for scanner.Scan() {
		if limited.N == 0 {
			return errors.New("测试流超过 4 MiB 总大小限制")
		}
		line := scanner.Text()
		if line == "" {
			if len(data) == 0 {
				continue
			}
			done, err := testEvent(format, []byte(strings.Join(data, "\n")))
			data = nil
			eventBytes = 0
			if err != nil {
				return err
			}
			if done {
				return nil
			}
		} else if strings.HasPrefix(line, "data:") {
			eventBytes += len(line) + 1
			if eventBytes > testEventLimit {
				return errors.New("测试流单事件超过 1 MiB 限制")
			}
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if limited.N == 0 {
		return errors.New("测试流超过 4 MiB 总大小限制")
	}
	if errors.Is(scanner.Err(), bufio.ErrTooLong) {
		return errors.New("测试流单行超过 1 MiB 限制")
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取测试流失败: %w", err)
	}
	return errors.New("测试流未正常结束")
}

func testEvent(format Format, data []byte) (bool, error) {
	if string(data) == "[DONE]" && format == FormatOpenAIChat {
		return true, nil
	}
	var event struct {
		Status   string          `json:"status"`
		Type     string          `json:"type"`
		Error    json.RawMessage `json:"error"`
		Response struct {
			Status string          `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"response"`
		Candidates []struct {
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if json.Unmarshal(data, &event) != nil {
		return false, errors.New("测试响应数据无效")
	}
	hasError := func(raw json.RawMessage) bool { return len(raw) > 0 && string(raw) != "null" }
	if hasError(event.Error) || hasError(event.Response.Error) || event.Type == "error" || event.Type == "response.failed" || event.Response.Status == "failed" || event.Status == "failed" {
		return false, errors.New("上游返回测试错误事件")
	}
	switch format {
	case FormatOpenAIResponses:
		return event.Type == "response.completed" || event.Type == "response.incomplete", nil
	case FormatAnthropic:
		return event.Type == "message_stop", nil
	case FormatGemini:
		for _, candidate := range event.Candidates {
			if candidate.FinishReason != "" {
				return true, nil
			}
		}
	}
	return false, nil
}
