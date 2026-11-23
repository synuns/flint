package testengine

import "os"

const dedicatedEngineDSN = "postgresql://flint:pw@127.0.0.1:5499/flint_test"

func TestDSNDefault() string {
	return dedicatedEngineDSN
}

func ResolveDSN() string {
	if dsn := os.Getenv("FLINT_TEST_DSN"); dsn != "" {
		return dsn
	}
	return dedicatedEngineDSN
}
