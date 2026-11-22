package xslices

// First 获取传入切片的首个元素，空切片返回元素零值。
func First[S ~[]E, E any](s S) (e E) {
	if len(s) > 0 {
		return s[0]
	}
	return
}
