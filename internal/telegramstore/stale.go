package telegramstore

// access_hash 过期后的自动恢复。
//
// 本地索引缓存了 access_hash，频道迁移、换 bot 账号后它会失效，
// Telegram 返回 CHANNEL_INVALID 等错误。这类错误说明请求没有生效，
// 可以清掉缓存、重新解析频道，再把同一个操作重跑一次（只一次）。
//
// 与 RPC 层重试的区别：重跑的是「整个操作」，所以 fn 内部会重新 resolve，
// 拿到的 access_hash 是新的；RPC 层重试只会带着旧 hash 再发一遍，没有意义。

// retryStale 执行 fn；access_hash 过期时清缓存后重跑一次。
//
// 输入：s 目标 store；fn 一次完整操作（内部自行 resolve）
// 输出：fn 的结果与错误
// 预期：只对 IsStaleChannel 的错误重试；第二次仍失败就原样返回
func retryStale[T any](s *Store, fn func() (T, error)) (T, error) {
	v, err := fn()
	if err == nil || !IsStaleChannel(err) {
		return v, err
	}
	s.dropChannel()
	return fn()
}

// retryStaleErr 是 retryStale 的无返回值版本。
//
// 输入：s 目标 store；fn 一次只返回 error 的完整操作
// 输出：fn 的错误
// 预期：与 retryStale 相同，access_hash 过期时重跑一次
func retryStaleErr(s *Store, fn func() error) error {
	_, err := retryStale(s, func() (struct{}, error) {
		return struct{}{}, fn()
	})
	return err
}
