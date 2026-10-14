package application

import (
	"crypto/sha256"
	"encoding/json"
)

// RequestHash — SHA-256 канонического JSON входа. Вход уже декодирован в структуру,
// поэтому порядок полей и пробелы исходного тела на хеш не влияют.
func RequestHash(in Input) []byte {
	// Input состоит из строк и чисел — Marshal не возвращает ошибку.
	b, _ := json.Marshal(in)
	sum := sha256.Sum256(b)
	return sum[:]
}
