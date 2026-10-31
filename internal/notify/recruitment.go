package notify

import (
	"fmt"
	"strings"
)

func recruitmentBody(f Facts) string {
	r := f.Recruitment
	body := fmt.Sprintf("🚨🚨 MAA 特别提醒｜发现 %d★ 公招机会\n账号：%s\n识别时间：%s\n标签：%s\n", r.Level, f.Account, localTime(r.ObservedAt), strings.Join(r.Tags, "、"))
	for _, c := range r.Combinations {
		body += fmt.Sprintf("%d★ 保底组合：%s\n", c.Level, strings.Join(c.Tags, " + "))
	}
	return body + "请特别留意并及时查看游戏/MAA！这是标签机会，尚未确认招募获得干员；招募时长、标签掉落与实际结果以游戏为准。原有公招选择、刷新和资源预算未改变。\n记录：" + f.ID
}
