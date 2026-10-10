package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 2
	argonSaltLength  = 16
	argonKeyLength   = 32
)

// 验证器接受的 Argon2id 参数范围，用来抵御恶意哈希造成的异常资源消耗。
// 哈希参数的校验与 PasswordParams 的合法性检查共用这些边界。
const (
	minArgonMemory      = 8 * 1024
	maxArgonMemory      = 256 * 1024
	minArgonIterations  = 1
	maxArgonIterations  = 10
	minArgonParallelism = 1
	maxArgonParallelism = 16
)

// PasswordParams 是 Argon2id 的成本参数；Memory 的单位为 KiB。
type PasswordParams struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
}

// DefaultPasswordParams 返回生产使用的参数（64 MiB、3 次迭代、并行度 2）。
func DefaultPasswordParams() PasswordParams {
	return PasswordParams{Memory: argonMemory, Iterations: argonIterations, Parallelism: argonParallelism}
}

// validate 要求参数落在验证器的接受范围内，使服务生成的哈希总能被验证。
func (p PasswordParams) validate() error {
	if p.Memory < minArgonMemory || p.Memory > maxArgonMemory ||
		p.Iterations < minArgonIterations || p.Iterations > maxArgonIterations ||
		p.Parallelism < minArgonParallelism || p.Parallelism > maxArgonParallelism {
		return fmt.Errorf("argon2id parameters m=%d,t=%d,p=%d are outside the range the verifier accepts", p.Memory, p.Iterations, p.Parallelism)
	}
	return nil
}

// HashPassword 使用生产参数哈希密码。
func HashPassword(password string) (string, error) {
	return hashPassword(password, DefaultPasswordParams())
}

func hashPassword(password string, params PasswordParams) (string, error) {
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, argonKeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		params.Memory,
		params.Iterations,
		params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	version, err := strconv.Atoi(strings.TrimPrefix(parts[2], "v="))
	if err != nil || version != argon2.Version {
		return false
	}
	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false
	}
	if (PasswordParams{Memory: memory, Iterations: iterations, Parallelism: parallelism}).validate() != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != argonKeyLength {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func ValidatePassword(password string) error {
	if len(password) < 12 {
		return errors.New("password must contain at least 12 characters")
	}
	if len(password) > 128 {
		return errors.New("password must contain at most 128 characters")
	}
	return nil
}

func GenerateInitialPassword() (string, error) {
	random := make([]byte, 18)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate initial password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}
