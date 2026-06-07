package common

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/golang-jwt/jwt/v4"
)

const (
	//短生命周期 JWT≈ 一次性签名认证票据
	jwtExpiryTimeout = 60 * time.Second
	JwtSecretLength  = 32
)

type JwtHandler struct {
	keyFunc func(token *jwt.Token) (interface{}, error)
	handler http.Handler
}

// 创建JWT中间件
func NewJwtHandler(handle http.Handler, jetSecretKey string) (http.Handler, error) {
	jwtSecret, err := hexutil.Decode(jetSecretKey)
	if err != nil {
		return nil, fmt.Errorf("invalid jwt secret %s", err.Error())
	}
	if len(jwtSecret) != JwtSecretLength {
		return nil, fmt.Errorf("invalid jwt secret length, expected length %d, actual length %d",
			JwtSecretLength, len(jwtSecret))
	}
	jwtHandler := &JwtHandler{
		keyFunc: func(token *jwt.Token) (interface{}, error) {
			return jwtSecret, nil
		}, //JWT库验证签名时会调用：keyFunc(token)拿到密钥
		handler: handle,
	}
	return jwtHandler, nil
}

// ServeHTTP implements http.Handler
func (h *JwtHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var (
		strToken string
		claims   jwt.RegisteredClaims
	)
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		strToken = strings.TrimPrefix(auth, "Bearer ")
	}
	if len(strToken) == 0 {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	token, err := jwt.ParseWithClaims(strToken, &claims, h.keyFunc,
		jwt.WithValidMethods([]string{"HS256"}), //只允许HS256
		jwt.WithoutClaimsValidation()) //关闭JWT库默认检查。

	switch {
	case err != nil:
		http.Error(w, err.Error(), http.StatusUnauthorized)
	case !token.Valid:
		http.Error(w, "invalid token", http.StatusUnauthorized)
	case !claims.VerifyExpiresAt(time.Now(), false):
		http.Error(w, "token is expired", http.StatusUnauthorized)
	case claims.IssuedAt == nil:
		http.Error(w, "missing issued-at", http.StatusUnauthorized)
	case time.Since(claims.IssuedAt.Time) > jwtExpiryTimeout:
		http.Error(w, "stale token", http.StatusUnauthorized)
	case time.Until(claims.IssuedAt.Time) > jwtExpiryTimeout:
		http.Error(w, "future token", http.StatusUnauthorized)
	default:
		h.handler.ServeHTTP(w, r)
	}
}
