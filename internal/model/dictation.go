package model

// DictationResult is the response body of POST /dictate (WL-299): the
// transcribed text of one recorded clip. It is also the field the ElevenLabs
// speech-to-text endpoint answers with, which lets the proxy decode the
// vendor response and answer the cockpit with one declaration.
type DictationResult struct {
	Text string `json:"text"`
}
