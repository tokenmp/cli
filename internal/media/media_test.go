package media

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestDecodeDataURL(t *testing.T) {
	pngBase64 := base64.StdEncoding.EncodeToString([]byte("fake-png-bytes"))

	cases := []struct {
		name       string
		dataURL    string
		wantMime   string
		wantBase64 string
	}{
		{
			name:       "带类型的 base64",
			dataURL:    "data:image/png;base64," + pngBase64,
			wantMime:   "image/png",
			wantBase64: pngBase64,
		},
		{
			name:       "没有声明类型时按 RFC 2397 用 text/plain",
			dataURL:    "data:;base64," + pngBase64,
			wantMime:   "text/plain",
			wantBase64: pngBase64,
		},
		{
			name:       "参数丢掉，只留裸类型",
			dataURL:    "data:text/plain;charset=utf-8;base64," + base64.StdEncoding.EncodeToString([]byte("你好")),
			wantMime:   "text/plain",
			wantBase64: base64.StdEncoding.EncodeToString([]byte("你好")),
		},
		{
			name:       "百分号编码的文本",
			dataURL:    "data:text/plain,%E4%BD%A0%E5%A5%BD%20world",
			wantMime:   "text/plain",
			wantBase64: base64.StdEncoding.EncodeToString([]byte("你好 world")),
		},
		{
			name:       "scheme 大小写不敏感",
			dataURL:    "DATA:image/png;BASE64," + pngBase64,
			wantMime:   "image/png",
			wantBase64: pngBase64,
		},
		{
			name:       "数据段里的空白与换行会被清掉",
			dataURL:    "data:image/png;base64," + pngBase64[:4] + "\n" + pngBase64[4:],
			wantMime:   "image/png",
			wantBase64: pngBase64,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mimeType, data, err := DecodeDataURL(testCase.dataURL)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if mimeType != testCase.wantMime {
				t.Errorf("类型是 %q，期望 %q", mimeType, testCase.wantMime)
			}
			if data != testCase.wantBase64 {
				t.Errorf("数据是 %q，期望 %q", data, testCase.wantBase64)
			}
		})
	}
}

func TestDecodeDataURLErrors(t *testing.T) {
	cases := []struct {
		name    string
		dataURL string
		want    string
	}{
		{"不是 data URL", "https://example.com/a.png", "不是 data URL"},
		{"缺少逗号", "data:image/png;base64", "逗号"},
		{"base64 不合法", "data:image/png;base64,不是base64!!", "base64"},
		{"百分号编码不合法", "data:text/plain,%ZZ", "百分号"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := DecodeDataURL(testCase.dataURL)
			if err == nil {
				t.Fatal("应当报错")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("报错是 %q，期望提到 %q", err, testCase.want)
			}
		})
	}
}

func TestParseHeader(t *testing.T) {
	cases := []struct {
		header     string
		wantMime   string
		wantBase64 bool
	}{
		{"image/png", "image/png", false},
		{"image/png;base64", "image/png", true},
		{"image/svg+xml;charset=utf-8", "image/svg+xml", false},
		{"", "text/plain", false},
	}
	for _, testCase := range cases {
		mimeType, isBase64 := parseHeader(testCase.header)
		if mimeType != testCase.wantMime || isBase64 != testCase.wantBase64 {
			t.Errorf("parseHeader(%q) = (%q, %v)，期望 (%q, %v)",
				testCase.header, mimeType, isBase64, testCase.wantMime, testCase.wantBase64)
		}
	}
}
