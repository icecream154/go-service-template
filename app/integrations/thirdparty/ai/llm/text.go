package llm

import (
	"errors"
	"slices"
	"strings"
)

// GenerateTextParams 表示单轮、非流式文本生成的输入，Prompt 原样交给模型。
type GenerateTextParams struct {
	// Model 每次调用必填，仅允许目标供应商支持的模型；Client 不补充默认值。
	Model  string
	Prompt string
}

// GenerateTextResult 只返回生成文本，不向业务层暴露 SDK 响应类型。
type GenerateTextResult struct {
	Content string
}

// validate 按供应商传入的模型列表精确校验，不改写参数或补充默认值。
func (params GenerateTextParams) validate(supportedModels ...string) error {
	if strings.TrimSpace(params.Model) == "" {
		return errors.New("LLM model is required")
	}
	if slices.Contains(supportedModels, params.Model) {
		return nil
	}
	return errors.New("LLM text generation model is not supported")
}
