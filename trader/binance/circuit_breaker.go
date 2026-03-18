package binance

import "nofx/binanceguard"

func CheckCircuitBreaker() error {
	return binanceguard.CheckCircuitBreaker()
}

func SetCircuitBreakerFromError(err error) {
	binanceguard.SetCircuitBreakerFromError(err)
}

func IsCircuitBreakerOpen(err error) bool {
	return binanceguard.IsCircuitBreakerOpen(err)
}

