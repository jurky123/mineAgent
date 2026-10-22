package storage

import (
	"strings"
	"testing"
)

func TestQQSpeakerLabelUsesIdentityNotNickname(t *testing.T) {
	first := QQSpeakerLabel("qq:member:G:A", "同名")
	second := QQSpeakerLabel("qq:member:G:B", "同名")
	if first == second || strings.Contains(first, "qq:member:G:A") {
		t.Fatal("name used as identity or raw openid leaked")
	}
	renamed := QQSpeakerLabel("qq:member:G:A", "新名")
	if strings.Split(first, "；")[0] != strings.Split(renamed, "；")[0] {
		t.Fatal("rename changed identity")
	}
	for _, id := range []string{"", "qq:group:G", "qq:group:G:conv-2", "qq:c2c:U"} {
		if !strings.Contains(QQSpeakerLabel(id, "群友"), "身份未知") {
			t.Fatal("legacy shared key treated as a person")
		}
	}
	if strings.Contains(QQSpeakerLabel("qq:user:A", "a\n[b]"), "\n") {
		t.Fatal("multiline nickname escaped label")
	}
}
