package publisher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
	"urban-radar/content"
)

type VKClient struct {
	Token   string
	GroupID int64
	HTTP    *http.Client
}

func NewVKClientFromEnv() (VKClient, error) {
	g, e := strconv.ParseInt(os.Getenv("VK_GROUP_ID"), 10, 64)
	if e != nil || g <= 0 || os.Getenv("VK_ACCESS_TOKEN") == "" {
		return VKClient{}, errors.New("VK_ACCESS_TOKEN and positive VK_GROUP_ID are required")
	}
	return VKClient{Token: os.Getenv("VK_ACCESS_TOKEN"), GroupID: g, HTTP: &http.Client{Timeout: 20 * time.Second}}, nil
}

type vkErr struct {
	ErrorCode int    `json:"error_code"`
	ErrorMsg  string `json:"error_msg"`
}

// VKAPIError is a definitive response from VK: the requested method was
// rejected and no caller should treat it as a transport ambiguity. Error()
// intentionally exposes only the stable numeric code, never response bodies
// or credentials.
type VKAPIError struct {
	Code    int
	Message string
}

func (e *VKAPIError) Error() string { return fmt.Sprintf("VK_API_ERROR_%d", e.Code) }

type vkResp struct {
	Response json.RawMessage `json:"response"`
	Error    *vkErr          `json:"error"`
}

func (c VKClient) api(ctx context.Context, method string, v url.Values, out any, finalSideEffect bool) error {
	v.Set("access_token", c.Token)
	v.Set("v", "5.199")
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.vk.com/method/"+method, bytes.NewBufferString(v.Encode()))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r, e := c.HTTP.Do(req)
	if e != nil {
		if finalSideEffect {
			return ambiguous{e}
		}
		return fmt.Errorf("VK_TRANSPORT_FAILURE")
	}
	defer r.Body.Close()
	b, e := io.ReadAll(r.Body)
	if e != nil {
		if finalSideEffect {
			return ambiguous{e}
		}
		return fmt.Errorf("VK_TRANSPORT_FAILURE")
	}
	var x vkResp
	if e = json.Unmarshal(b, &x); e != nil {
		return fmt.Errorf("VK_INVALID_RESPONSE")
	}
	if x.Error != nil {
		if x.Error.ErrorCode <= 0 {
			return fmt.Errorf("VK_INVALID_RESPONSE")
		}
		return &VKAPIError{Code: x.Error.ErrorCode, Message: x.Error.ErrorMsg}
	}
	if len(x.Response) == 0 || bytes.Equal(x.Response, []byte("null")) {
		return fmt.Errorf("VK_INVALID_RESPONSE")
	}
	if e = json.Unmarshal(x.Response, out); e != nil {
		return fmt.Errorf("VK_INVALID_RESPONSE")
	}
	return nil
}

type ambiguous struct{ error }

func (ambiguous) AmbiguousPublish() bool { return true }
func (c VKClient) Publish(ctx context.Context, text string, m *content.PublishMedia) (string, error) {
	attach := ""
	if m != nil {
		var u struct {
			UploadURL string `json:"upload_url"`
		}
		if e := c.api(ctx, "photos.getWallUploadServer", url.Values{"group_id": {strconv.FormatInt(c.GroupID, 10)}}, &u, false); e != nil {
			return "", e
		}
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		f, e := w.CreateFormFile("photo", "upload.jpg")
		if e != nil {
			return "", e
		}
		_, _ = f.Write(m.Bytes)
		w.Close()
		q, e := http.NewRequestWithContext(ctx, http.MethodPost, u.UploadURL, &b)
		if e != nil {
			return "", e
		}
		q.Header.Set("Content-Type", w.FormDataContentType())
		r, e := c.HTTP.Do(q)
		if e != nil {
			return "", e
		}
		defer r.Body.Close()
		var up struct {
			Server int    `json:"server"`
			Photo  string `json:"photo"`
			Hash   string `json:"hash"`
		}
		if e = json.NewDecoder(r.Body).Decode(&up); e != nil {
			return "", e
		}
		var saved []struct {
			OwnerID   int64  `json:"owner_id"`
			ID        int64  `json:"id"`
			AccessKey string `json:"access_key"`
		}
		if e = c.api(ctx, "photos.saveWallPhoto", url.Values{"group_id": {strconv.FormatInt(c.GroupID, 10)}, "server": {strconv.Itoa(up.Server)}, "photo": {up.Photo}, "hash": {up.Hash}}, &saved, false); e != nil || len(saved) != 1 {
			return "", e
		}
		attach = fmt.Sprintf("photo%d_%d", saved[0].OwnerID, saved[0].ID)
		if saved[0].AccessKey != "" {
			attach += "_" + saved[0].AccessKey
		}
	}
	// VK API v5.199 returns {"response":{"post_id":<positive integer>}}
	// for wall.post. It is not a bare integer like some older examples imply.
	var response struct {
		PostID int64 `json:"post_id"`
	}
	e := c.api(ctx, "wall.post", url.Values{"owner_id": {"-" + strconv.FormatInt(c.GroupID, 10)}, "from_group": {"1"}, "message": {text}, "attachments": {attach}}, &response, true)
	if e != nil {
		return "", e
	}
	if response.PostID <= 0 {
		return "", fmt.Errorf("VK_INVALID_RESPONSE")
	}
	return strconv.FormatInt(response.PostID, 10), nil
}
