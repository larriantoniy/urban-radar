package publisher

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"urban-radar/content"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func vkTestClient(body string) VKClient {
	return VKClient{
		Token:   "test-token",
		GroupID: 123,
		HTTP: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/method/wall.post" {
				return nil, errors.New("unexpected VK method")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		})},
	}
}

func TestVKPublishParsesWallPostObjectResponse(t *testing.T) {
	client := vkTestClient(`{"response":{"post_id":987}}`)

	id, err := client.Publish(context.Background(), "text", nil)
	if err != nil {
		t.Fatal(err)
	}
	if id != "987" {
		t.Fatalf("post ID = %q, want 987", id)
	}
}

func TestVKAPIErrorResponseIsTyped(t *testing.T) {
	client := vkTestClient(`{"error":{"error_code":15,"error_msg":"access denied"}}`)

	_, err := client.Publish(context.Background(), "text", nil)
	var apiErr *VKAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want VKAPIError", err)
	}
	if apiErr.Code != 15 || apiErr.Method != "wall.post" || apiErr.Message != "access denied" {
		t.Fatalf("API error = %+v", apiErr)
	}
	if err.Error() != "VK_API_ERROR_15:wall.post" {
		t.Fatalf("safe error = %q", err)
	}
}

func TestVKPublishRejectsMalformedWallPostResponse(t *testing.T) {
	client := vkTestClient(`{"response":{"post_id":"not-a-number"}}`)

	_, err := client.Publish(context.Background(), "text", nil)
	if err == nil || err.Error() != "VK_INVALID_RESPONSE" {
		t.Fatalf("error = %v, want VK_INVALID_RESPONSE", err)
	}
}

func photoTestClient(t *testing.T, fn func(*http.Request) string) VKClient {
	t.Helper()
	return VKClient{
		Token: "test-token", GroupID: 123,
		HTTP: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(fn(request)))}, nil
		})},
	}
}

func requestValues(t *testing.T, request *http.Request) url.Values {
	t.Helper()
	b, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	v, err := url.ParseQuery(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func assertPhotoUpload(t *testing.T, request *http.Request) {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("content type = %q, err=%v", mediaType, err)
	}
	form, err := multipart.NewReader(request.Body, params["boundary"]).ReadForm(1024 * 1024)
	if err != nil {
		t.Fatal(err)
	}
	files := form.File["photo"]
	if len(files) != 1 || files[0].Filename != "upload.jpg" {
		t.Fatalf("uploaded files = %+v", files)
	}
	f, err := files[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(b, []byte("image-bytes")) {
		t.Fatalf("upload bytes = %q, err=%v", b, err)
	}
}

func TestVKPublishPhotoFlow(t *testing.T) {
	var calls []string
	client := photoTestClient(t, func(request *http.Request) string {
		calls = append(calls, request.URL.Path)
		switch request.URL.Path {
		case "/method/photos.getWallUploadServer":
			v := requestValues(t, request)
			if v.Get("group_id") != "123" || v.Get("access_token") != "test-token" || v.Get("v") != "5.199" {
				t.Fatalf("upload server params = %v", v)
			}
			return `{"response":{"upload_url":"https://upload.test/wall"}}`
		case "/wall":
			assertPhotoUpload(t, request)
			return `{"server":1,"photo":"photo-data","hash":"hash-data"}`
		case "/method/photos.saveWallPhoto":
			v := requestValues(t, request)
			if v.Get("group_id") != "123" || v.Get("server") != "1" || v.Get("photo") != "photo-data" || v.Get("hash") != "hash-data" {
				t.Fatalf("save params = %v", v)
			}
			return `{"response":[{"owner_id":-123,"id":55,"access_key":"key"}]}`
		case "/method/wall.post":
			v := requestValues(t, request)
			if v.Get("owner_id") != "-123" || v.Get("from_group") != "1" || v.Get("attachments") != "photo-123_55_key" {
				t.Fatalf("wall params = %v", v)
			}
			return `{"response":{"post_id":987}}`
		default:
			t.Fatalf("unexpected request path %q", request.URL.Path)
			return ""
		}
	})

	id, err := client.Publish(context.Background(), "text", &content.PublishMedia{Bytes: []byte("image-bytes")})
	if err != nil || id != "987" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if got, want := strings.Join(calls, ","), "/method/photos.getWallUploadServer,/wall,/method/photos.saveWallPhoto,/method/wall.post"; got != want {
		t.Fatalf("call order = %q, want %q", got, want)
	}
}

func TestVKPublishPhotoPreservesAPIErrorStage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failAt string
		want   string
	}{
		{name: "upload server", failAt: "/method/photos.getWallUploadServer", want: "VK_API_ERROR_27:photos.getWallUploadServer"},
		{name: "save photo", failAt: "/method/photos.saveWallPhoto", want: "VK_API_ERROR_27:photos.saveWallPhoto"},
		{name: "wall post", failAt: "/method/wall.post", want: "VK_API_ERROR_27:wall.post"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := photoTestClient(t, func(request *http.Request) string {
				if request.URL.Path == tc.failAt {
					return `{"error":{"error_code":27,"error_msg":"group authorization failed"}}`
				}
				switch request.URL.Path {
				case "/method/photos.getWallUploadServer":
					return `{"response":{"upload_url":"https://upload.test/wall"}}`
				case "/wall":
					return `{"server":1,"photo":"photo-data","hash":"hash-data"}`
				case "/method/photos.saveWallPhoto":
					return `{"response":[{"owner_id":-123,"id":55}]}`
				default:
					t.Fatalf("unexpected request path %q", request.URL.Path)
					return ""
				}
			})
			_, err := client.Publish(context.Background(), "text", &content.PublishMedia{Bytes: []byte("image-bytes")})
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			var apiErr *VKAPIError
			if !errors.As(err, &apiErr) || apiErr.Method == "" {
				t.Fatalf("typed API error = %+v", apiErr)
			}
		})
	}
}
