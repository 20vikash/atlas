package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/labstack/echo/v4"
)

const maximumJSONBodyBytes = 1 << 20

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
