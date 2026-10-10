// Package media 提供跨协议转换需要的媒体解析回调。
//
// 内核自己不发网络请求，两个回调都由宿主提供。这里只实现解析 data URL 的那半：
// 内容已经躺在请求里，解析不碰网络，因此没有内网地址与体积上限这类问题。
//
// 按 URL 取回内容的那半没有实现，GetBase64Data 留空。需要它的转换会明确报错
// 「媒体解析回调未注册」，不会静默丢掉图片。做那半要先定重定向策略、体积上限、
// 超时，以及怎么挡住指向内网地址的请求。
package media

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/tokenmp/core/relay"
)

// dataURLPrefix 是 data URL 的固定前缀。scheme 大小写不敏感，比较时先归一。
const dataURLPrefix = "data:"

// defaultMimeType 是 data URL 没有声明类型时按 RFC 2397 使用的类型。
const defaultMimeType = "text/plain"

// Register 注册媒体解析能力。
//
// 这是进程级的全局设置，只能在启动阶段调用一次。
func Register() {
	relay.SetMediaResolver(relay.MediaResolver{DecodeBase64FileData: DecodeDataURL})
}

// DecodeDataURL 解析一段 data URL，返回它的 MIME 类型与 base64 数据。
//
// 两种写法都认：带 ;base64 的数据段是 base64，不带的是按 RFC 2397 百分号编码的
// 文本。返回的数据一律是重新编码过的 base64，因此不带换行与空白，可以安全地嵌进
// 目标请求的 JSON。
func DecodeDataURL(dataURL string) (mimeType string, base64Data string, err error) {
	if !strings.HasPrefix(strings.ToLower(dataURL), dataURLPrefix) {
		return "", "", errors.New("媒体解析: 不是 data URL")
	}

	header, payload, ok := strings.Cut(dataURL[len(dataURLPrefix):], ",")
	if !ok {
		return "", "", errors.New("媒体解析: data URL 缺少分隔头部与数据段的逗号")
	}

	mimeType, isBase64 := parseHeader(header)
	if isBase64 {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
		if err != nil {
			return "", "", fmt.Errorf("媒体解析: data URL 的 base64 数据不合法: %w", err)
		}
		return mimeType, base64.StdEncoding.EncodeToString(decoded), nil
	}

	text, err := url.PathUnescape(payload)
	if err != nil {
		return "", "", fmt.Errorf("媒体解析: data URL 的百分号编码不合法: %w", err)
	}
	return mimeType, base64.StdEncoding.EncodeToString([]byte(text)), nil
}

// parseHeader 从逗号之前的部分取出 MIME 类型，并报告数据段是不是 base64。
//
// 头部形如 <类型>/<子类型>[;参数][;base64]，参数（例如 charset）丢掉不要，返回的
// 是裸类型。
func parseHeader(header string) (mimeType string, isBase64 bool) {
	parts := strings.Split(header, ";")
	mimeType = strings.TrimSpace(parts[0])
	if mimeType == "" {
		mimeType = defaultMimeType
	}
	for _, part := range parts[1:] {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			isBase64 = true
		}
	}
	return mimeType, isBase64
}
