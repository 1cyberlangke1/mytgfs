package config

// 写 [telegram] 段的受管键。
//
// 面板里填 api_id 时只改这一行，注释、其它键、段顺序保持原样。

import (
	"errors"
	"strconv"

	"mytgfs/internal/i18n"
)

// SetTelegramAPIID 写入 [telegram].api_id。
//
// 输入：path 配置文件；id Telegram api_id（必须 > 0）
// 输出：error
// 预期：段内其余内容逐字节保留；写入走临时文件 + rename
func SetTelegramAPIID(path string, id int) error {
	if id <= 0 {
		return errors.New(i18n.T("err.bad_api_id"))
	}
	return rewriteSection(path, "telegram", []string{"api_id = " + strconv.Itoa(id)}, nil)
}
