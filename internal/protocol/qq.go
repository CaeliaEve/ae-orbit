package protocol

import (
	"fmt"
	"regexp"
)

var qqNumber = regexp.MustCompile(`^[1-9][0-9]{4,14}$`)

func QQNumberValid(value string) bool { return qqNumber.MatchString(value) }

type QQNotification struct {
	GroupID     string `json:"group_id"`
	Recipient   string `json:"recipient"`
	PrivateCopy bool   `json:"private_copy"`
}

func (n QQNotification) Validate() error {
	if !QQNumberValid(n.GroupID) || n.Recipient != "" && !QQNumberValid(n.Recipient) || n.PrivateCopy && n.Recipient == "" {
		return fmt.Errorf("invalid QQ notification target")
	}
	return nil
}
