package api

import (
	"context"
	"encoding/json"
	"io"

	"github.com/coder/websocket"
	"github.com/labstack/echo/v4"

	"github.com/frappe/atlas/metal/internal/console"
)

// resizeQueueDepth is how many pending resize requests a viewer may have.
const resizeQueueDepth = 4

// consoleControlMessage carries a viewer request sent as a text frame.
type consoleControlMessage struct {
	Resize *struct {
		Cols uint16 `json:"cols"`
		Rows uint16 `json:"rows"`
	} `json:"resize"`
}

// @Summary	Open a virtual machine console
// @Description	Upgrade the request to a WebSocket for the serial console or an SSH session.
// @ID			getVirtualMachineConsole
// @Tags		Virtual machines
// @Security	BearerAuth
// @Param		id		path	string	true	"Virtual machine identifier"
// @Param		mode	query	string	false	"Console mode" Enums(tty, ssh) default(tty)
// @Success	101	"Switching protocols"
// @Failure	400	{object}	errorResponse
// @Failure	401	{object}	errorResponse
// @Failure	404	{object}	errorResponse
// @Failure	500	{object}	errorResponse
// @Router		/v1/vms/{id}/console [get]
func (s *Server) getVirtualMachineConsole(c echo.Context) error {
	id := c.Param("id")
	if !validResourceID(id) {
		return badRequest("invalid virtual machine identifier")
	}

	// Default options reject cross-origin browsers.
	connection, err := websocket.Accept(c.Response(), c.Request(), nil)
	if err != nil {
		return nil
	}
	defer connection.CloseNow()

	ctx, cancel := context.WithCancel(c.Request().Context())
	defer cancel()

	if c.QueryParam("mode") == "ssh" {
		s.streamSSHConsole(ctx, connection, id)
	} else {
		s.streamTTYConsole(ctx, connection, id)
	}

	return nil
}

// websocketTerminal presents a WebSocket as the byte stream a terminal session
// reads and writes. Binary frames carry terminal bytes in both directions. Text
// frames carry control messages. Both console modes use it, so a viewer sees the
// same framing whichever mode it opens.
type websocketTerminal struct {
	ctx        context.Context
	connection *websocket.Conn
	input      *io.PipeReader
	resize     chan console.Winsize
}

// newWebSocketTerminal wraps connection and starts reading viewer frames.
func newWebSocketTerminal(ctx context.Context, connection *websocket.Conn) *websocketTerminal {
	inputReader, inputWriter := io.Pipe()
	terminal := &websocketTerminal{
		ctx:        ctx,
		connection: connection,
		input:      inputReader,
		resize:     make(chan console.Winsize, resizeQueueDepth),
	}
	go terminal.readFrames(inputWriter)

	return terminal
}

// Read returns the viewer keystrokes received so far.
func (terminal *websocketTerminal) Read(buffer []byte) (int, error) {
	return terminal.input.Read(buffer)
}

// Write sends session output to the viewer as one binary frame.
func (terminal *websocketTerminal) Write(data []byte) (int, error) {
	if err := terminal.connection.Write(terminal.ctx, websocket.MessageBinary, data); err != nil {
		return 0, err
	}

	return len(data), nil
}

// readFrames routes binary frames to the input pipe and text frames to resize.
// It closes the pipe when the viewer disconnects, which stops the session.
func (terminal *websocketTerminal) readFrames(input *io.PipeWriter) {
	for {
		messageType, data, err := terminal.connection.Read(terminal.ctx)
		if err != nil {
			_ = input.CloseWithError(err)
			return
		}

		switch messageType {
		case websocket.MessageBinary:
			if _, err := input.Write(data); err != nil {
				return
			}
		case websocket.MessageText:
			terminal.queueResize(data)
		}
	}
}

// queueResize parses a control message and queues any resize it requests. A full
// queue means the viewer is already resizing faster than the session applies it,
// so the request is dropped.
func (terminal *websocketTerminal) queueResize(data []byte) {
	var message consoleControlMessage
	if json.Unmarshal(data, &message) != nil || message.Resize == nil {
		return
	}

	select {
	case terminal.resize <- console.Winsize{Rows: message.Resize.Rows, Cols: message.Resize.Cols}:
	default:
	}
}
