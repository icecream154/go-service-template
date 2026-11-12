package service

import (
	"errors"
	"testing"

	"go-service-template/internal/check"
)

type generateUserAvatarParamsTestCase struct {
	name     string
	prompt   string
	wantRule string
}

// TestValidateGenerateUserAvatarParams 验证头像用例的提示词约束。
func TestValidateGenerateUserAvatarParams(t *testing.T) {
	tests := []generateUserAvatarParamsTestCase{
		// 有实际内容的提示词带有首尾空格，仍应通过非空白检查。
		{name: "nonblank prompt", prompt: "  一只戴眼镜的橘猫  "},
		// 全角空格和制表符都属于空白，组合起来仍然没有可用内容。
		{name: "blank prompt", prompt: "\u3000\t", wantRule: "not_blank"},
		// 直接调用 Service 可以传入非法 UTF-8 字节，应返回编码校验错误。
		{name: "invalid UTF-8", prompt: "猫\xff", wantRule: "valid_utf8"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateGenerateUserAvatarParams(GenerateUserAvatarParams{Prompt: tt.prompt})
			if tt.wantRule != "" {
				assertInvalidAvatarPrompt(t, err, tt.wantRule)
				return
			}
			if err != nil {
				t.Errorf("validateGenerateUserAvatarParams() = %v, want nil", err)
			}
		})
	}
}

// TestGenerateUserAvatarRejectsEmptyPrompt 验证公开入口在访问 Client 前拒绝缺失的提示词。
func TestGenerateUserAvatarRejectsEmptyPrompt(t *testing.T) {
	// 包级 TestMain 已初始化 Logger 和 Tracer。两个远端 Client 保持未初始化，
	// 因此空提示词若被错误放行，访问共享 Client 时测试就会失败。
	result, err := NewUserProfileService().GenerateUserAvatar(t.Context(), GenerateUserAvatarParams{})
	if result != nil {
		t.Errorf("GenerateUserAvatar result = %+v, want nil", result)
	}
	assertInvalidAvatarPrompt(t, err, "not_blank")
}

// assertInvalidAvatarPrompt 核对输入错误的公共类别和具体规则。
func assertInvalidAvatarPrompt(t *testing.T, err error, wantRule string) {
	t.Helper()
	var violation *check.Violation
	if !errors.Is(err, check.ErrInvalidValue) || !errors.As(err, &violation) || violation.Rule != wantRule {
		t.Errorf("avatar prompt error = %v, want %v with rule %q", err, check.ErrInvalidValue, wantRule)
	}
}
