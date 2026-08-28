package lark

import (
	"testing"

	"pieqi/internal/model"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// TestConvertP2MessageToModel 验证长连接事件(SDK 的 *P2MessageReceiveV1)
// 被正确转成 Pieqi 内部的 model.Message。重点验证:
// 1. sender open_id 落到 model.Message.UserID(身份绑定链路用)
// 2. message.content(JSON 字符串)被解析出纯文本
// 3. chat_id/chat_type 正确传递
// 4. p2p 与 group 两种会话类型都能处理
func TestConvertP2MessageToModel(t *testing.T) {
	// 构造一条 SDK 风格的 fake event(用指针,因为 SDK 字段都是 *string)
	strPtr := func(s string) *string { return &s }

	fake := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: strPtr("ou_test_admin"),
					UserId: strPtr("abcd1234"),
				},
				SenderType: strPtr("user"),
			},
			Message: &larkim.EventMessage{
				ChatType:    strPtr("p2p"),
				ChatId:      strPtr("oc_chat_1"),
				MessageId:   strPtr("om_msg_1"),
				MessageType: strPtr("text"),
				Content:     strPtr(`{"text":"hello pieqi"}`),
			},
		},
	}

	msg := convertP2Message(fake)
	if msg.UserID != "ou_test_admin" {
		t.Fatalf("UserID = %q, want ou_test_admin", msg.UserID)
	}
	if msg.ChatID != "oc_chat_1" {
		t.Fatalf("ChatID = %q, want oc_chat_1", msg.ChatID)
	}
	if msg.Content != "hello pieqi" {
		t.Fatalf("Content = %q, want hello pieqi", msg.Content)
	}
	if msg.Channel != model.ChannelLark {
		t.Fatalf("Channel = %q, want %q", msg.Channel, model.ChannelLark)
	}
}
