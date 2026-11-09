package userprofile

// GenerateUserAvatarRequest 表示根据提示词生成用户头像的请求，不修改已保存的用户资料。
type GenerateUserAvatarRequest struct {
	// Prompt 必填，必须是非空白的 UTF-8 文本，原样交给图片生成服务。
	Prompt string `form:"prompt" json:"prompt"`
}

// GenerateUserAvatarResponse 返回本次生成的头像地址。
type GenerateUserAvatarResponse struct {
	// AvatarURL 是图片生成服务返回的图片地址，不自动写入用户资料。
	AvatarURL string `json:"avatar_url"`
}
