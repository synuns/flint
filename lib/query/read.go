// Package query는 read 질의 조립을 제공한다.
package query

// ClampLimit은 조회 limit을 허용 범위로 고정한다. (T8에서 구현, 예: 999->50, 0->10)
func ClampLimit(n int) int {
	panic("not implemented: ClampLimit (T8)")
}
