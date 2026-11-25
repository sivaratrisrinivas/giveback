// Package telegram is the small part of the Telegram Bot API Giveback uses.
// Updates arrive by webhook (never polling), so the Sprite can sleep between them.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"` // a tap on one of our buttons
}

// CallbackQuery is a tap on an inline button.
type CallbackQuery struct {
	ID      string   `json:"id"`
	Data    string   `json:"data"`    // what we put in the button
	Message *Message `json:"message"` // the message the button was on
}

// Button is one inline button; Data comes back in the CallbackQuery (max 64 bytes).
// A Button with no Text (NewRow) starts a new row of buttons.
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

// NewRow goes between buttons to put the next ones on a row of their own.
var NewRow = Button{}

func keyboard(buttons []Button) string {
	rows := [][]Button{{}}
	for _, b := range buttons {
		if b.Text == "" {
			if len(rows[len(rows)-1]) > 0 {
				rows = append(rows, []Button{})
			}
			continue
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], b)
	}
	if len(rows) > 1 && len(rows[len(rows)-1]) == 0 {
		rows = rows[:len(rows)-1]
	}
	if len(rows) == 1 && len(rows[0]) == 0 {
		rows = [][]Button{} // no buttons: clears the keyboard
	}
	b, _ := json.Marshal(map[string]any{"inline_keyboard": rows})
	return string(b)
}

type Message struct {
	MessageID int64       `json:"message_id"`
	Date      int64       `json:"date"` // Unix seconds when Telegram received it
	Text      string      `json:"text"`
	Caption   string      `json:"caption"`
	Photo     []PhotoSize `json:"photo"`    // sizes of one photo, smallest first
	Document  *Document   `json:"document"` // a photo sent "as file" keeps full resolution
	Chat      struct {
		ID int64 `json:"id"`
	} `json:"chat"`
}

type PhotoSize struct {
	FileID string `json:"file_id"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MIMEType string `json:"mime_type"`
}

// ImageFileID returns the file to download if the message carries an image.
func (m *Message) ImageFileID() (string, bool) {
	if n := len(m.Photo); n > 0 {
		return m.Photo[n-1].FileID, true // the largest size
	}
	if d := m.Document; d != nil && strings.HasPrefix(d.MIMEType, "image/") {
		return d.FileID, true
	}
	return "", false
}

type Client struct {
	token string
	http  *http.Client
}

func New(token string) *Client {
	return &Client{token: token, http: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	return c.SendText(ctx, chatID, text, nil)
}

// SendText sends a message with a row of buttons under it (none if nil).
func (c *Client) SendText(ctx context.Context, chatID int64, text string, buttons []Button) error {
	p := map[string]any{"chat_id": chatID, "text": text}
	if buttons != nil {
		p["reply_markup"] = json.RawMessage(keyboard(buttons))
	}
	return c.call(ctx, "sendMessage", p, nil)
}

// AnswerCallback stops the button's spinner and shows a short notice.
func (c *Client) AnswerCallback(ctx context.Context, id, text string) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}

// ClearButtons removes the buttons from a message once they are answered.
func (c *Client) ClearButtons(ctx context.Context, chatID, messageID int64) error {
	return c.call(ctx, "editMessageReplyMarkup", map[string]any{"chat_id": chatID, "message_id": messageID,
		"reply_markup": json.RawMessage(keyboard(nil))}, nil)
}

// SendChatAction shows "typing…" or "sending photo…" while we work.
func (c *Client) SendChatAction(ctx context.Context, chatID int64, action string) error {
	return c.call(ctx, "sendChatAction", map[string]any{"chat_id": chatID, "action": action}, nil)
}

// SendPhoto uploads a local image with a caption and a row of buttons (none if nil).
func (c *Client) SendPhoto(ctx context.Context, chatID int64, path, caption string, buttons []Button) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	w.WriteField("chat_id", fmt.Sprint(chatID))
	w.WriteField("caption", caption)
	if buttons != nil {
		w.WriteField("reply_markup", keyboard(buttons))
	}
	part, err := w.CreateFormFile("photo", filepath.Base(path))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, f); err != nil {
		return err
	}
	w.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("sendPhoto"), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return c.do(req, "sendPhoto", nil)
}

// Download fetches a file the user sent.
func (c *Client) Download(ctx context.Context, fileID string) ([]byte, error) {
	var f struct {
		FilePath string `json:"file_path"`
	}
	if err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.telegram.org/file/bot"+c.token+"/"+f.FilePath, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.redact(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: telegram returned %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 25<<20))
}

func (c *Client) url(method string) string {
	return "https://api.telegram.org/bot" + c.token + "/" + method
}

func (c *Client) call(ctx context.Context, method string, params map[string]any, result any) error {
	b, _ := json.Marshal(params)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(method), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, method, result)
}

func (c *Client) do(req *http.Request, method string, result any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		// The URL holds the bot token; never let it reach the logs.
		return fmt.Errorf("%s: %w", method, c.redact(err))
	}
	defer resp.Body.Close()
	var r struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("%s: telegram returned %s", method, resp.Status)
	}
	if !r.OK {
		return fmt.Errorf("%s: %s", method, r.Description)
	}
	if result != nil {
		return json.Unmarshal(r.Result, result)
	}
	return nil
}

func (c *Client) redact(err error) error {
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), c.token, "<token>"))
}
