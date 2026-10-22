package storage

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
)

// QQSpeakerLabel keeps display names separate from the platform identity. The
// short identifier is stable across renames/forks, without exposing OpenIDs.
// Old releases stored the conversation key as AuthorID; those rows are unknown.
func QQSpeakerLabel(id, name string) string {
	person := "身份未知（旧记录或平台未提供）"
	if strings.HasPrefix(id, "qq:member:") || strings.HasPrefix(id, "qq:user:") || strings.HasPrefix(id, "qq:union:") {
		sum := sha256.Sum256([]byte(id))
		person = fmt.Sprintf("user-%x", sum[:8])
	}
	r := []rune(name)
	if len(r) > 80 {
		r = r[:80]
	}
	return "QQ发言者 " + person + "；昵称=" + strconv.Quote(string(r))
}
