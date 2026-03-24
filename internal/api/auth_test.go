package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireScope(t *testing.T) {
	tests := []struct {
		name          string
		userScopes    []string
		requiredScope string
		expectedCode  int
	}{
		{
			name:          "admin gets access to anything",
			userScopes:    []string{"admin"},
			requiredScope: "projects:delete",
			expectedCode:  http.StatusOK,
		},
		{
			name:          "exact scope gets access",
			userScopes:    []string{"projects:read", "projects:delete"},
			requiredScope: "projects:delete",
			expectedCode:  http.StatusOK,
		},
		{
			name:          "missing scope gets forbidden",
			userScopes:    []string{"projects:read"},
			requiredScope: "projects:delete",
			expectedCode:  http.StatusForbidden,
		},
		{
			name:          "no scopes gets forbidden",
			userScopes:    []string{},
			requiredScope: "projects:delete",
			expectedCode:  http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			middleware := RequireScope(tt.requiredScope)
			handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			
			authCtx := AuthContext{
				UserID: 0,
				Scopes: tt.userScopes,
			}
			ctx := context.WithValue(req.Context(), userContextKey, authCtx)
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.expectedCode {
				t.Errorf("expected status %v; got %v", tt.expectedCode, rr.Code)
			}
		})
	}
}
