package terminal

import (
	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/fileutil"
	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/profile"
)

func readSubscriptionFile(path string) (struct {
	Content string
	Exists  bool
}, error) {
	content, _, exists, err := fileutil.ReadRegular(path, profile.MaxSize)
	return struct {
		Content string
		Exists  bool
	}{string(content), exists}, err
}
