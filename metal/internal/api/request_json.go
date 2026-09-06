package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/labstack/echo/v4"
)

// maximumJSONBodyBytes caps a request body, so a large upload cannot exhaust
// host memory before it is rejected.
const maximumJSONBodyBytes = 1 << 20

// decodeJSONRequest decodes one JSON body. Unknown fields and trailing values
// are rejected, so a misspelled field fails instead of being silently ignored.
func decodeJSONRequest(c echo.Context, value any) error {
	request := c.Request()
	request.Body = http.MaxBytesReader(c.Response(), request.Body, maximumJSONBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return badRequest("invalid JSON request")
	}

	var trailing any
	err := decoder.Decode(&trailing)
	if !errors.Is(err, io.EOF) {
		return badRequest("invalid JSON request")
	}
	return nil
}
