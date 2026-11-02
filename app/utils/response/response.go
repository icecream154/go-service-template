package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	CodeOK               = 200
	CodeInvalidParameter = -400300
	CodeInternalError    = -500100
)

type Response struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data"`
}

// ReturnJson 保留 HTTP 状态与业务码的区别，失败数据统一为空。
func ReturnJson(c *gin.Context, httpCode, dataCode int, msg string, data any) {
	if dataCode != CodeOK {
		data = nil
	}
	c.JSON(httpCode, Response{Code: dataCode, Msg: msg, Data: data})
}

func Success(c *gin.Context, code int, msg string, data any) {
	ReturnJson(c, http.StatusOK, code, msg, data)
}

// ErrorParam 不把绑定细节写入对外响应，保持参数错误契约。
func ErrorParam(c *gin.Context, _ any) {
	ReturnJson(c, http.StatusOK, CodeInvalidParameter, "参数校验失败", nil)
	c.Abort()
}

func ErrorSystem(c *gin.Context) {
	ReturnJson(c, http.StatusInternalServerError, CodeInternalError, "内部服务错误", nil)
	c.Abort()
}
