# jp-municipality-domains

日本の地方自治体（都道府県・市区町村）の公式ウェブサイトのドメインを JSON 形式でまとめたデータセットです。

全1,788件（都道府県および市区町村）のレコードを `data.json` に収録しています。
ご自由にご利用ください。

## データ構造

`data.json`は以下の構造体に対応するオブジェクトの配列です。

```typescript
type Municipality = {
  // 公式ウェブサイトのURL
  url: string;

  // 併用されているURL（存在する場合のみ）
  sub_url?: string;

  // 全国地方公共団体コード
  municipality_code: number;

  // 市区町村名（半角カナ）。都道府県レコードでは null
  municipality_name_kana: string | null;

  // 市区町村名（漢字）。都道府県レコードでは null
  municipality_name_kanji: string | null;

  // 都道府県名（半角カナ）
  prefecture_name_kana: string;

  // 都道府県名（漢字）
  prefecture_name_kanji: string;
};
```
