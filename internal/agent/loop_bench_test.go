package agent

import (
	"testing"
)

func BenchmarkIsEcho(b *testing.B) {
	input := "hello how are you doing today"
	spoken := "hello how are you doing today my friend"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isEcho(input, spoken)
	}
}

func BenchmarkIsEcho_ShortInput(b *testing.B) {
	input := "yes"
	spoken := "would you like me to open chrome"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isEcho(input, spoken)
	}
}

func BenchmarkIsEchoStrict(b *testing.B) {
	input := "hello how are you"
	spoken := "hello how are you doing today"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isEchoStrict(input, spoken)
	}
}

func BenchmarkCleanResponse(b *testing.B) {
	response := "Here is the response:\n```json\n{\"thought\":\"hello\",\"action\":\"test\"}\n```\nSome trailing text"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cleanResponse(response)
	}
}
