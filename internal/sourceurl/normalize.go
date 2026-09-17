// Package sourceurl provides the canonical identity used for browser-clipped pages.
package sourceurl

import (
	"net/url"
	"path"
	"strings"
)

// Normalize removes presentation/share noise while preserving query parameters
// that can identify a page. Keep this as the single Go-side implementation so
// ingest and filesystem rescans cannot assign different source_url_norm keys.
func Normalize(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(raw, "/")
	}

	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	host := strings.ToLower(u.Hostname())

	if videoID := youtubeVideoID(u, host); videoID != "" {
		return "https://www.youtube.com/watch?v=" + url.QueryEscape(videoID)
	}

	switch {
	case isWeChatArticle(host, u.Path), isXStatus(host, u.Path):
		u.RawQuery = ""
	default:
		query := u.Query()
		for _, key := range trackingQueryKeys {
			query.Del(key)
		}
		u.RawQuery = query.Encode()
	}

	u.Path = path.Clean("/" + strings.TrimPrefix(u.Path, "/"))
	if u.Path == "/" {
		u.Path = ""
	} else {
		u.Path = strings.TrimRight(u.Path, "/")
	}
	return u.String()
}

var trackingQueryKeys = []string{
	"from", "scene", "sessionid", "ascene", "devicetype", "version",
	"nettype", "abtest_cookie", "wx_header", "poc_token", "srcid",
	"sharer_shareinfo", "sharer_shareinfo_first", "mpshare", "clicktime",
	"enterid", "exportkey", "pass_ticket", "uin", "key", "wxtoken",
}

func isWeChatArticle(host, pathname string) bool {
	return strings.Contains(host, "weixin.qq.com") ||
		(strings.Contains(host, "qq.com") && strings.HasPrefix(pathname, "/s"))
}

func isXStatus(host, pathname string) bool {
	if host != "x.com" && host != "www.x.com" && host != "twitter.com" &&
		host != "www.twitter.com" && host != "mobile.twitter.com" {
		return false
	}
	parts := strings.Split(strings.Trim(pathname, "/"), "/")
	return len(parts) >= 3 && parts[1] == "status" && parts[2] != ""
}

func youtubeVideoID(u *url.URL, host string) string {
	var id string
	switch host {
	case "youtu.be", "www.youtu.be":
		id = strings.Split(strings.Trim(u.Path, "/"), "/")[0]
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 1 && parts[0] == "watch" {
			id = u.Query().Get("v")
		} else if len(parts) >= 2 && (parts[0] == "shorts" || parts[0] == "embed") {
			id = parts[1]
		}
	}
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "/?#&") {
		return ""
	}
	return id
}
