package apierror_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-service-template/app/domains/user/apierror"
	usererrors "go-service-template/app/domains/user/errors"
	"go-service-template/internal/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestFromError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantCode     response.ErrorCode
		wantBusiness response.BusinessErrorCode
		wantMessage  string
		wantStatus   int
		wantField    string
		wantReason   string
	}{
		{
			name: "invalid user ID", err: usererrors.ErrInvalidUserID,
			wantCode: response.CodeInvalidArgument, wantMessage: "invalid request parameters",
			wantStatus: http.StatusBadRequest, wantField: "user_id",
		},
		{
			name: "wrapped invalid user ID", err: fmt.Errorf("validate query: %w", usererrors.ErrInvalidUserID),
			wantCode: response.CodeInvalidArgument, wantMessage: "invalid request parameters",
			wantStatus: http.StatusBadRequest, wantField: "user_id",
		},
		{
			name: "profile not found", err: usererrors.ErrUserProfileNotFound,
			wantCode: response.CodeNotFound, wantBusiness: apierror.CodeUserProfileNotFound,
			wantMessage: "user profile not found", wantStatus: http.StatusNotFound,
		},
		{
			name: "wrapped profile not found", err: fmt.Errorf("query profile: %w", usererrors.ErrUserProfileNotFound),
			wantCode: response.CodeNotFound, wantBusiness: apierror.CodeUserProfileNotFound,
			wantMessage: "user profile not found", wantStatus: http.StatusNotFound,
		},
		{
			name: "wrapped field validation", err: fmt.Errorf("create user: %w", &usererrors.InvalidArgumentError{Field: "user_name", Reason: "must not be blank"}),
			wantCode: response.CodeInvalidArgument, wantMessage: "invalid request parameters",
			wantStatus: http.StatusBadRequest, wantField: "user_name", wantReason: "must not be blank",
		},
		{
			name: "account already exists", err: fmt.Errorf("create user: %w", usererrors.ErrAccountAlreadyExists),
			wantCode: response.CodeConflict, wantBusiness: apierror.CodeAccountAlreadyExists,
			wantMessage: "user account already exists", wantStatus: http.StatusConflict,
		},
		{
			name: "disabled user", err: usererrors.ErrUserDisabled,
			wantCode: response.CodeConflict, wantBusiness: apierror.CodeUserDisabled,
			wantMessage: "user is disabled", wantStatus: http.StatusConflict,
		},
		{
			name: "version conflict", err: fmt.Errorf("update profile: %w", usererrors.ErrUserProfileVersionConflict),
			wantCode: response.CodeConflict, wantBusiness: apierror.CodeUserProfileVersionConflict,
			wantMessage: "user profile version conflict", wantStatus: http.StatusConflict,
		},
		{
			name: "another user profile", err: usererrors.ErrUserProfilePermissionDenied,
			wantCode: response.CodePermissionDenied, wantBusiness: apierror.CodeUserProfilePermissionDenied,
			wantMessage: "user profile operation is not permitted", wantStatus: http.StatusForbidden,
		},
		{
			name: "unrecognized error", err: errors.New("private database diagnostic"),
			wantCode: response.CodeInternalError, wantMessage: "internal server error",
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "persistence error must be translated before adaptation", err: gorm.ErrRecordNotFound,
			wantCode: response.CodeInternalError, wantMessage: "internal server error",
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "nil error", err: nil,
			wantCode: response.CodeInternalError, wantMessage: "internal server error",
			wantStatus: http.StatusInternalServerError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := apierror.FromError(test.err)
			if failure.Code != test.wantCode || failure.BusinessCode != test.wantBusiness || failure.Message != test.wantMessage {
				t.Fatalf("unexpected mapping: %+v", failure)
			}
			if test.wantField == "" {
				if len(failure.Details) != 0 {
					t.Fatalf("unexpected field details: %+v", failure.Details)
				}
			} else {
				wantReason := test.wantReason
				if wantReason == "" {
					wantReason = "must be a positive 64-bit integer"
				}
				if len(failure.Details) != 1 || failure.Details[0].Field != test.wantField || failure.Details[0].Reason != wantReason {
					t.Fatalf("unexpected field details: %+v", failure.Details)
				}
			}

			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			response.Fail(ctx, failure)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
			if strings.Contains(recorder.Body.String(), "private database diagnostic") {
				t.Fatalf("internal error leaked into response: %s", recorder.Body.String())
			}
		})
	}
}
