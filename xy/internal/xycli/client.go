package xycli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is an ordinary xy client that authenticates with an API token
// (ADR-0015) instead of a session cookie.
type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

func NewClient(base, token string) *Client {
	return &Client{
		Base:  strings.TrimRight(base, "/"),
		Token: token,
		// Generous: an export renders typst server-side, and split-fit is slow.
		HTTP: &http.Client{Timeout: 5 * time.Minute},
	}
}

// apiError carries the server's own message, which is written for a human.
type apiError struct {
	Status int
	Msg    string
	Path   string
}

func (e *apiError) Error() string {
	if e.Msg == "" {
		return fmt.Sprintf("%s: %d", e.Path, e.Status)
	}
	return fmt.Sprintf("%s: %d %s", e.Path, e.Status, e.Msg)
}

func (c *Client) request(method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequest(method, c.Base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		text := serverMessage(msg)
		if resp.StatusCode == http.StatusUnauthorized {
			text = "токен не принят: истёк, отозван или сменился пароль аккаунта — `xy-cli login` заново"
		}
		return nil, &apiError{Status: resp.StatusCode, Msg: text, Path: path}
	}
	return resp, nil
}

// serverMessage unwraps httpError's {"error": "…"} into the sentence it holds.
func serverMessage(raw []byte) string {
	var wrapped struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Error != "" {
		return wrapped.Error
	}
	return strings.TrimSpace(string(raw))
}

// do sends an optional JSON body and decodes an optional JSON response.
func (c *Client) do(method, path string, in, out any) error {
	var body io.Reader
	contentType := ""
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body, contentType = bytes.NewReader(raw), "application/json"
	}
	resp, err := c.request(method, path, body, contentType)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) get(path string, out any) error { return c.do("GET", path, nil, out) }

// bytesOf fetches a raw body (an attachment's ciphertext, an export's file).
func (c *Client) bytesOf(path string) ([]byte, error) {
	resp, err := c.request("GET", path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// postForm sends a multipart body and returns the raw response, which for the
// export endpoints is the file itself; filename is taken from the disposition.
func (c *Client) postForm(path string, build func(*multipart.Writer) error) ([]byte, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := build(mw); err != nil {
		return nil, "", err
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	resp, err := c.request("POST", path, &buf, mw.FormDataContentType())
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return data, dispositionName(resp.Header.Get("Content-Disposition")), err
}

// dispositionName reads the filename an export was served under, preferring the
// RFC 5987 form the server writes for non-ASCII names.
func dispositionName(header string) string {
	_, params, err := mime.ParseMediaType(header)
	if err != nil {
		return ""
	}
	if v, ok := params["filename*"]; ok {
		if decoded, err := url.QueryUnescape(strings.TrimPrefix(v, "UTF-8''")); err == nil {
			return decoded
		}
	}
	return params["filename"]
}

// ---- wire shapes (mirroring internal/server's DTOs) ----

type BoardSummary struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	NameEnc        string `json:"name_enc"`
	SchemaVersion  int    `json:"schema_version"`
	Role           string `json:"role"`
	Unread         bool   `json:"unread"`
	UnreadMentions bool   `json:"unread_mentions"`
}

type listDTO struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	TitleEnc string `json:"title_enc"`
	Rank     string `json:"rank"`
	GroupID  *int64 `json:"group_id,omitempty"`
}

type groupDTO struct {
	ID      int64  `json:"id"`
	NameEnc string `json:"name_enc"`
}

type cardDTO struct {
	ID        int64   `json:"id"`
	ListID    int64   `json:"list_id"`
	Kind      string  `json:"kind"`
	DescEnc   string  `json:"description_enc"`
	Rank      string  `json:"rank"`
	AliasEnc  *string `json:"alias_enc,omitempty"`
	CreatedAt string  `json:"created_at"`
}

type labelDTO struct {
	ID       int64  `json:"id"`
	NameEnc  string `json:"name_enc"`
	ColorEnc string `json:"color_enc"`
}

type cardLabelDTO struct {
	CardID    int64  `json:"card_id"`
	LabelID   int64  `json:"label_id"`
	SessionID *int64 `json:"session_id,omitempty"`
}

type snapshotDTO struct {
	ID         int64          `json:"id"`
	Name       string         `json:"name"`
	Role       string         `json:"role"`
	Lists      []listDTO      `json:"lists"`
	Groups     []groupDTO     `json:"groups"`
	Cards      []cardDTO      `json:"cards"`
	Labels     []labelDTO     `json:"labels"`
	CardLabels []cardLabelDTO `json:"card_labels"`
}

// TimelineEvent is one лента entry as the API returns it (ciphertext payload).
type TimelineEvent struct {
	ID         int64   `json:"id"`
	Type       string  `json:"type"`
	AuthorID   *int64  `json:"author_user_id"`
	CreatedAt  string  `json:"created_at"`
	EditedAt   *string `json:"edited_at,omitempty"`
	ReplyToID  *int64  `json:"reply_to_id,omitempty"`
	ReplyCount int     `json:"reply_count"`
	Deleted    bool    `json:"deleted,omitempty"`
	PayloadEnc string  `json:"payload_enc"`
	CardID     *int64  `json:"card_id,omitempty"`
}

type boardCommentDTO struct {
	ID         int64  `json:"id"`
	CardID     int64  `json:"card_id"`
	PayloadEnc string `json:"payload_enc"`
}

type MemberDTO struct {
	UserID   int64   `json:"user_id"`
	Role     string  `json:"role"`
	Username *string `json:"username"`
}

// AttachmentDTO is one attachment's metadata; the bytes come separately.
type AttachmentDTO struct {
	ID          int64  `json:"id"`
	FilenameEnc string `json:"filename_enc"`
	Mime        string `json:"mime"`
	Size        int64  `json:"size"`
	IsExcerpt   bool   `json:"is_excerpt"`
	CreatedAt   string `json:"created_at"`
	Rev         int64  `json:"rev"`
}

type idResponse struct {
	ID int64 `json:"id"`
}

// ---- calls ----

func (c *Client) Me() (username string, err error) {
	var me struct {
		UserID   int64   `json:"user_id"`
		Username *string `json:"username"`
	}
	if err := c.get("/api/auth/me", &me); err != nil {
		return "", err
	}
	if me.Username != nil {
		return *me.Username, nil
	}
	return "", nil
}

func (c *Client) Boards() ([]BoardSummary, error) {
	var out []BoardSummary
	return out, c.get("/api/boards", &out)
}

func (c *Client) Keymeta(boardID int64) (Keymeta, error) {
	var km Keymeta
	return km, c.get(fmt.Sprintf("/api/boards/%d/keymeta", boardID), &km)
}

func (c *Client) Snapshot(boardID int64) (snapshotDTO, error) {
	var snap snapshotDTO
	return snap, c.get(fmt.Sprintf("/api/boards/%d", boardID), &snap)
}

func (c *Client) Members(boardID int64) ([]MemberDTO, error) {
	var out []MemberDTO
	return out, c.get(fmt.Sprintf("/api/boards/%d/members", boardID), &out)
}

func (c *Client) BoardComments(boardID int64) ([]boardCommentDTO, error) {
	var out []boardCommentDTO
	return out, c.get(fmt.Sprintf("/api/boards/%d/comments", boardID), &out)
}

func (c *Client) Timeline(cardID int64) ([]TimelineEvent, error) {
	var out []TimelineEvent
	return out, c.get(fmt.Sprintf("/api/cards/%d/timeline", cardID), &out)
}

func (c *Client) CreateList(boardID int64, titleEnc, rank string) (int64, error) {
	var out idResponse
	err := c.do("POST", fmt.Sprintf("/api/boards/%d/lists", boardID),
		map[string]any{"title_enc": titleEnc, "rank": rank, "type": "normal"}, &out)
	return out.ID, err
}

func (c *Client) PatchList(listID int64, body map[string]any) error {
	return c.do("PATCH", fmt.Sprintf("/api/lists/%d", listID), body, nil)
}

func (c *Client) DeleteList(listID int64) error {
	return c.do("DELETE", fmt.Sprintf("/api/lists/%d", listID), nil, nil)
}

func (c *Client) CreateCard(listID int64, body map[string]any) (int64, error) {
	var out idResponse
	err := c.do("POST", fmt.Sprintf("/api/lists/%d/cards", listID), body, &out)
	return out.ID, err
}

func (c *Client) PatchCard(cardID int64, body map[string]any) error {
	return c.do("PATCH", fmt.Sprintf("/api/cards/%d", cardID), body, nil)
}

func (c *Client) DeleteCard(cardID int64) error {
	return c.do("DELETE", fmt.Sprintf("/api/cards/%d", cardID), nil, nil)
}

func (c *Client) AddComment(cardID int64, body map[string]any) error {
	return c.do("POST", fmt.Sprintf("/api/cards/%d/comments", cardID), body, nil)
}

func (c *Client) PatchComment(commentID int64, body map[string]any) error {
	return c.do("PATCH", fmt.Sprintf("/api/comments/%d", commentID), body, nil)
}

func (c *Client) DeleteComment(commentID int64) error {
	return c.do("DELETE", fmt.Sprintf("/api/comments/%d", commentID), nil, nil)
}

func (c *Client) CreateLabel(boardID int64, nameEnc, colorEnc string) (int64, error) {
	var out idResponse
	err := c.do("POST", fmt.Sprintf("/api/boards/%d/labels", boardID),
		map[string]any{"name_enc": nameEnc, "color_enc": colorEnc}, &out)
	return out.ID, err
}

// SetCardLabels writes a card's whole assignment set, as the web client does,
// with the label_add/label_remove entries that put the change in the лента.
func (c *Client) SetCardLabels(cardID int64, labels []cardLabelDTO, events []map[string]any) error {
	items := make([]map[string]any, 0, len(labels))
	for _, l := range labels {
		item := map[string]any{"label_id": l.LabelID}
		if l.SessionID != nil {
			item["session_id"] = *l.SessionID
		}
		items = append(items, item)
	}
	return c.do("PUT", fmt.Sprintf("/api/cards/%d/labels", cardID),
		map[string]any{"labels": items, "events": events}, nil)
}

func (c *Client) Attachments(cardID int64) ([]AttachmentDTO, error) {
	var out []AttachmentDTO
	return out, c.get(fmt.Sprintf("/api/cards/%d/attachments", cardID), &out)
}

func (c *Client) AttachmentBytes(id int64) ([]byte, error) {
	return c.bytesOf(fmt.Sprintf("/api/attachments/%d", id))
}

// UploadAttachment posts ciphertext bytes plus the encrypted filename, and the
// «file» timeline entry that tells the лента an attachment arrived.
func (c *Client) UploadAttachment(cardID int64, meta map[string]any, blob []byte) (int64, error) {
	var out idResponse
	data, _, err := c.postForm(fmt.Sprintf("/api/cards/%d/attachments", cardID), func(mw *multipart.Writer) error {
		raw, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		if err := mw.WriteField("meta", string(raw)); err != nil {
			return err
		}
		part, err := mw.CreateFormFile("blob", "blob")
		if err != nil {
			return err
		}
		_, err = part.Write(blob)
		return err
	})
	if err != nil {
		return 0, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// ExportPack renders a 4s source into the requested formats server-side; the
// answer is the bare file when one format was asked for, a zip when several.
func (c *Client) ExportPack(source, name, formats string, images map[string][]byte) ([]byte, string, error) {
	return c.postForm("/api/export/pack", func(mw *multipart.Writer) error {
		for _, field := range [][2]string{{"source", source}, {"filename", name}, {"formats", formats}} {
			if err := mw.WriteField(field[0], field[1]); err != nil {
				return err
			}
		}
		for filename, data := range images {
			part, err := mw.CreateFormFile("img", filename)
			if err != nil {
				return err
			}
			if _, err := part.Write(data); err != nil {
				return err
			}
		}
		return nil
	})
}
