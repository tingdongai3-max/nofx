package provider

const providerBodyTailRunes = 1024

func providerBodyTail(body []byte) string {
	runes := []rune(string(body))
	if len(runes) <= providerBodyTailRunes {
		return string(runes)
	}
	return string(runes[len(runes)-providerBodyTailRunes:])
}
