package municipality

import "fmt"

type Municipality struct {
	URL                   string `json:"url"`
	Code                  int    `json:"municipality_code"`
	PrefectureNameKanji   string `json:"prefecture_name_kanji"`
	MunicipalityNameKanji string `json:"municipality_name_kanji"`
}

func (m Municipality) BuildQuery() string {
	if m.MunicipalityNameKanji != "" {
		return fmt.Sprintf("%s %s ホームページ", m.PrefectureNameKanji, m.MunicipalityNameKanji)
	}
	return fmt.Sprintf("%s ホームページ", m.PrefectureNameKanji)
}
