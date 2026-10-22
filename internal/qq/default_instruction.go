package qq

// DefaultCapabilities 返回 QQ 通道的内置系统提示词（能力/规则部分）。
func DefaultCapabilities() string {
	return qqInstruction + "\n\n" + qqInputCapabilities + "\n\n" + qqConversationStyle
}
