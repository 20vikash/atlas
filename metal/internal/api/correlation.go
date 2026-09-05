package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"

	"github.com/labstack/echo/v4"
)

type correlationKey string

const (
	requestIDKey   correlationKey = "request_id"
	operationIDKey correlationKey = "operation_id"
)

var safeCorrelationID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func withCorrelation(requestContext context.Context, requestID, operationID string) context.Context {
	requestContext = context.WithValue(requestContext, requestIDKey, requestID)
	return context.WithValue(requestContext, operationIDKey, operationID)
}

func requestID(requestContext context.Context) string {
	value, _ := requestContext.Value(requestIDKey).(string)
	return value
}

func operationID(requestContext context.Context) string {
	value, _ := requestContext.Value(operationIDKey).(string)
	return value
}

func correlationMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		requestID := correlationHeader(c.Request(), "X-Request-ID")
		if requestID == "" {
			requestID = newCorrelationID()
		}
		operationID := correlationHeader(c.Request(), "X-Operation-ID")
		if operationID == "" {
			operationID = newCorrelationID()
		}
		c.SetRequest(c.Request().WithContext(withCorrelation(c.Request().Context(), requestID, operationID)))
		c.Response().Header().Set("X-Request-ID", requestID)
		c.Response().Header().Set("X-Operation-ID", operationID)
		return next(c)
	}
}

func correlationHeader(request *http.Request, name string) string {
	value := request.Header.Get(name)
	if safeCorrelationID.MatchString(value) {
		return value
	}
	return ""
}

func newCorrelationID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(value)
}
