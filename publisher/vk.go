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
		return fmt.Errorf("VK_API_ERROR_%d", x.Error.ErrorCode)
	}
	return json.Unmarshal(x.Response, out)
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
	var id int64
	e := c.api(ctx, "wall.post", url.Values{"owner_id": {"-" + strconv.FormatInt(c.GroupID, 10)}, "from_group": {"1"}, "message": {text}, "attachments": {attach}}, &id, true)
	if e != nil {
		return "", e
	}
	return strconv.FormatInt(id, 10), nil
}
