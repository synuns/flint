// Package store는 kind별 검증·정규화·해시를 제공한다.
package store

import "fmt"

// Validate는 kind 입력값의 필수 필드를 검증한다. (T3에서 구현)
func Validate(kind string, p map[string]any) error {
	return fmt.Errorf("not implemented: Validate (T3)")
}
