package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/fogleman/gg"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

const (
	width  = 5120
	height = 2880
)

// Star はHYGデータベースの1つの星を表す。
type Star struct {
	HIP    int
	Proper string
	RA     float64 // 赤経（時）
	Dec    float64 // 赤緯（度）
	Mag    float64
	Con    string
	Bayer  string
	CI     float64 // 色指数
}

// StellariumData はStellariumのJSONのトップレベル構造。
type StellariumData struct {
	Constellations []ConstellationData `json:"constellations"`
}

// ConstellationData はStellariumの1つの星座データを表す。
type ConstellationData struct {
	ID         string `json:"id"`
	Lines      [][]int
	CommonName struct {
		English string `json:"english"`
		Native  string `json:"native"`
	} `json:"common_name"`
}

func main() {
	stars, err := loadStars("data/hygdata_v41.csv")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load stars: %v\n", err)
		os.Exit(1)
	}

	hipIndex := make(map[int]*Star)
	conStars := make(map[string][]*Star)
	for i := range stars {
		s := &stars[i]
		if s.HIP > 0 {
			hipIndex[s.HIP] = s
		}
		if s.Con != "" {
			conStars[s.Con] = append(conStars[s.Con], s)
		}
	}

	constellations, err := loadConstellations("data/stellarium_modern.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load constellations: %v\n", err)
		os.Exit(1)
	}

	os.MkdirAll("output", 0o755)

	for _, con := range constellations {
		abbr := conAbbr(con.ID)
		jpName := constellationJP[abbr]
		if jpName == "" {
			jpName = con.CommonName.English
		}

		fmt.Printf("Generating %s (%s)...\n", abbr, jpName)
		err := renderConstellation(con, abbr, jpName, hipIndex, conStars[abbr], stars)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  error: %v\n", err)
		}
	}
}

func loadStars(path string) ([]Star, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		return nil, err
	}

	colIndex := make(map[string]int)
	for i, h := range header {
		colIndex[strings.TrimSpace(h)] = i
	}

	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}

	var stars []Star
	for _, rec := range records {
		hip, _ := strconv.Atoi(rec[colIndex["hip"]])
		ra, _ := strconv.ParseFloat(rec[colIndex["ra"]], 64)
		dec, _ := strconv.ParseFloat(rec[colIndex["dec"]], 64)
		mag, _ := strconv.ParseFloat(rec[colIndex["mag"]], 64)
		ci, _ := strconv.ParseFloat(rec[colIndex["ci"]], 64)
		stars = append(stars, Star{
			HIP:    hip,
			Proper: rec[colIndex["proper"]],
			RA:     ra,
			Dec:    dec,
			Mag:    mag,
			Con:    rec[colIndex["con"]],
			Bayer:  rec[colIndex["bayer"]],
			CI:     ci,
		})
	}
	return stars, nil
}

func loadConstellations(path string) ([]ConstellationData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sd StellariumData
	if err := json.Unmarshal(data, &sd); err != nil {
		return nil, err
	}
	return sd.Constellations, nil
}

func conAbbr(id string) string {
	parts := strings.Split(id, " ")
	return parts[len(parts)-1]
}

func renderConstellation(con ConstellationData, abbr, jpName string, hipIndex map[int]*Star, starsInCon []*Star, allStars []Star) error {
	// この星座の線で使われるHIP IDを収集する。
	lineHIPs := make(map[int]bool)
	for _, line := range con.Lines {
		for _, hip := range line {
			lineHIPs[hip] = true
		}
	}

	// 星座線の星からバウンディングボックスを求める。
	var raMin, raMax, decMin, decMax float64
	first := true
	for hip := range lineHIPs {
		s, ok := hipIndex[hip]
		if !ok {
			continue
		}
		if first {
			raMin, raMax = s.RA, s.RA
			decMin, decMax = s.Dec, s.Dec
			first = false
		} else {
			raMin = math.Min(raMin, s.RA)
			raMax = math.Max(raMax, s.RA)
			decMin = math.Min(decMin, s.Dec)
			decMax = math.Max(decMax, s.Dec)
		}
	}

	if first {
		return fmt.Errorf("no stars found for %s", abbr)
	}

	// 赤経の折り返し処理（0h をまたぐ星座の場合）。
	raWrap := false
	raRange := raMax - raMin
	if raRange > 12 {
		raWrap = true
		raMin, raMax = 999.0, -999.0
		for hip := range lineHIPs {
			s, ok := hipIndex[hip]
			if !ok {
				continue
			}
			ra := s.RA
			if ra < 12 {
				ra += 24
			}
			raMin = math.Min(raMin, ra)
			raMax = math.Max(raMax, ra)
		}
	}

	// 余白を追加する。
	raPad := (raMax - raMin) * 0.3
	decPad := (decMax - decMin) * 0.3
	// 最小余白。
	if raPad < 0.5 {
		raPad = 0.5
	}
	if decPad < 3.0 {
		decPad = 3.0
	}
	raMin -= raPad
	raMax += raPad
	decMin -= decPad
	decMax += decPad

	// アスペクト比を出力画像に合わせる。
	raSpan := raMax - raMin
	decSpan := decMax - decMin
	targetRatio := float64(width) / float64(height)
	// 赤経は時間単位、1h ≈ 15°。度に変換してアスペクト比を計算する。
	raSpanDeg := raSpan * 15.0
	currentRatio := raSpanDeg / decSpan
	if currentRatio < targetRatio {
		// 赤経方向を広げる。
		needed := targetRatio * decSpan / 15.0
		diff := needed - raSpan
		raMin -= diff / 2
		raMax += diff / 2
		raSpan = raMax - raMin
	} else {
		// 赤緯方向を広げる。
		needed := raSpanDeg / targetRatio
		diff := needed - decSpan
		decMin -= diff / 2
		decMax += diff / 2
		decSpan = decMax - decMin
	}

	// 赤経・赤緯からピクセル座標への投影関数。
	project := func(ra, dec float64) (float64, float64) {
		// 折り返し処理。
		if raWrap && ra < 12 {
			ra += 24
		}
		// 赤経は天球上で右から左へ増加するので反転する。
		x := float64(width) * (1.0 - (ra-raMin)/raSpan)
		y := float64(height) * (1.0 - (dec-decMin)/decSpan)
		return x, y
	}

	dc := gg.NewContext(width, height)

	// 背景色: 濃紺。
	dc.SetColor(color.RGBA{R: 8, G: 10, B: 28, A: 255})
	dc.Clear()

	// 表示範囲内の全天の星を背景として描画する。
	for i := range allStars {
		s := &allStars[i]
		if s.Mag > 7.5 || s.HIP == 0 {
			continue
		}
		x, y := project(s.RA, s.Dec)
		if x < -10 || x > float64(width)+10 || y < -10 || y > float64(height)+10 {
			continue
		}
		if lineHIPs[s.HIP] {
			continue
		}
		drawStar(dc, x, y, s.Mag, false)
	}

	// 星座線を描画する。
	dc.SetColor(color.RGBA{R: 50, G: 70, B: 140, A: 90})
	dc.SetLineWidth(3.0)
	for _, line := range con.Lines {
		for i := 0; i < len(line)-1; i++ {
			s1, ok1 := hipIndex[line[i]]
			s2, ok2 := hipIndex[line[i+1]]
			if !ok1 || !ok2 {
				continue
			}
			x1, y1 := project(s1.RA, s1.Dec)
			x2, y2 := project(s2.RA, s2.Dec)
			dc.DrawLine(x1, y1, x2, y2)
			dc.Stroke()
		}
	}

	// 星座を構成する星を目立つように描画する。
	for hip := range lineHIPs {
		s, ok := hipIndex[hip]
		if !ok {
			continue
		}
		x, y := project(s.RA, s.Dec)
		drawStar(dc, x, y, s.Mag, true)
	}

	// 固有名のある星にラベルを描画する。
	starNameFace := loadFontFace("/System/Library/Fonts/SFNS.ttf", 42)
	if starNameFace != nil {
		dc.SetFontFace(starNameFace)
	}
	dc.SetColor(color.RGBA{R: 160, G: 170, B: 200, A: 180})
	for hip := range lineHIPs {
		s, ok := hipIndex[hip]
		if !ok || s.Proper == "" {
			continue
		}
		x, y := project(s.RA, s.Dec)
		r := starRadius(s.Mag)*1.3 + 8
		dc.DrawStringAnchored(s.Proper, x+r, y, 0, 0.5)
	}

	// 星座名を描画する。
	jpFace := loadFontFace("/System/Library/Fonts/AppleSDGothicNeo.ttc", 72)
	if jpFace != nil {
		dc.SetFontFace(jpFace)
	}
	dc.SetColor(color.RGBA{R: 140, G: 150, B: 190, A: 160})
	dc.DrawStringAnchored(jpName, float64(width)/2, float64(height)-120, 0.5, 0.5)

	return dc.SavePNG(fmt.Sprintf("output/%s.png", abbr))
}

// drawStar は白い点としてガウシアングローつきで星を描画する。
func drawStar(dc *gg.Context, x, y, mag float64, prominent bool) {
	coreR := 8.0 * math.Pow(10, -0.10*(mag+1.5))
	if prominent {
		coreR *= 1.8
		if coreR < 3.5 {
			coreR = 3.5
		}
	} else {
		coreR *= 0.9
		if coreR < 1.0 {
			coreR = 1.0
		}
	}

	// グロー: モアレを避けるためピクセル単位で直接書き込む。
	if prominent && mag < 3.0 {
		glowR := coreR * 5.0
		if rgba, ok := dc.Image().(*image.RGBA); ok {
			ix := int(x)
			iy := int(y)
			r := int(glowR) + 2
			for dy := -r; dy <= r; dy++ {
				for dx := -r; dx <= r; dx++ {
					dist := math.Sqrt(float64(dx*dx + dy*dy))
					if dist > glowR {
						continue
					}
					t := dist / glowR
					a := 0.18 * math.Exp(-5.0*t*t)
					if a < 0.003 {
						continue
					}
					px, py := ix+dx, iy+dy
					if px < 0 || px >= width || py < 0 || py >= height {
						continue
					}
					off := rgba.PixOffset(px, py)
					// 白を加算合成する。
					add := uint16(a * 255)
					rgba.Pix[off+0] = uint8(min(255, uint16(rgba.Pix[off+0])+add))
					rgba.Pix[off+1] = uint8(min(255, uint16(rgba.Pix[off+1])+add))
					rgba.Pix[off+2] = uint8(min(255, uint16(rgba.Pix[off+2])+add))
				}
			}
		}
	}

	// 星の本体: 塗りつぶし円。
	brightness := 1.0
	if !prominent {
		brightness = 0.4 + 0.6*math.Max(0, 1.0-mag/7.0)
	}
	v := uint8(brightness * 255)
	dc.SetColor(color.RGBA{R: v, G: v, B: v, A: 255})
	dc.DrawCircle(x, y, coreR)
	dc.Fill()
}

func starRadius(mag float64) float64 {
	r := 8.0 * math.Pow(10, -0.10*(mag+1.5)) * 1.8
	if r < 3.5 {
		r = 3.5
	}
	return r
}

var constellationJP = map[string]string{
	"And": "アンドロメダ座", "Ant": "ポンプ座", "Aps": "ふうちょう座", "Aqr": "みずがめ座",
	"Aql": "わし座", "Ara": "さいだん座", "Ari": "おひつじ座", "Aur": "ぎょしゃ座",
	"Boo": "うしかい座", "Cae": "ちょうこくぐ座", "Cam": "きりん座", "Cnc": "かに座",
	"CVn": "りょうけん座", "CMa": "おおいぬ座", "CMi": "こいぬ座", "Cap": "やぎ座",
	"Car": "りゅうこつ座", "Cas": "カシオペヤ座", "Cen": "ケンタウルス座", "Cep": "ケフェウス座",
	"Cet": "くじら座", "Cha": "カメレオン座", "Cir": "コンパス座", "Col": "はと座",
	"Com": "かみのけ座", "CrA": "みなみのかんむり座", "CrB": "かんむり座", "Crv": "からす座",
	"Crt": "コップ座", "Cru": "みなみじゅうじ座", "Cyg": "はくちょう座", "Del": "いるか座",
	"Dor": "かじき座", "Dra": "りゅう座", "Equ": "こうま座", "Eri": "エリダヌス座",
	"For": "ろ座", "Gem": "ふたご座", "Gru": "つる座", "Her": "ヘルクレス座",
	"Hor": "とけい座", "Hya": "うみへび座", "Hyi": "みずへび座", "Ind": "インディアン座",
	"Lac": "とかげ座", "Leo": "しし座", "LMi": "こじし座", "Lep": "うさぎ座",
	"Lib": "てんびん座", "Lup": "おおかみ座", "Lyn": "やまねこ座", "Lyr": "こと座",
	"Men": "テーブルさん座", "Mic": "けんびきょう座", "Mon": "いっかくじゅう座", "Mus": "はえ座",
	"Nor": "じょうぎ座", "Oct": "はちぶんぎ座", "Oph": "へびつかい座", "Ori": "オリオン座",
	"Pav": "くじゃく座", "Peg": "ペガスス座", "Per": "ペルセウス座", "Phe": "ほうおう座",
	"Pic": "がか座", "Psc": "うお座", "PsA": "みなみのうお座", "Pup": "とも座",
	"Pyx": "らしんばん座", "Ret": "レチクル座", "Sge": "や座", "Sgr": "いて座",
	"Sco": "さそり座", "Scl": "ちょうこくしつ座", "Sct": "たて座", "Ser": "へび座",
	"Sex": "ろくぶんぎ座", "Tau": "おうし座", "Tel": "ぼうえんきょう座", "Tri": "さんかく座",
	"TrA": "みなみのさんかく座", "Tuc": "きょしちょう座", "UMa": "おおぐま座", "UMi": "こぐま座",
	"Vel": "ほ座", "Vir": "おとめ座", "Vol": "とびうお座", "Vul": "こぎつね座",
}

func loadFontFace(path string, size float64) font.Face {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: cannot read font %s: %v\n", path, err)
		return nil
	}

	// まずTTCとして試し、失敗したら単体フォントとしてパースする。
	col, err := opentype.ParseCollection(data)
	if err == nil {
		f, err := col.Font(0)
		if err == nil {
			face, err := opentype.NewFace(f, &opentype.FaceOptions{
				Size:    size,
				DPI:     72,
				Hinting: font.HintingFull,
			})
			if err == nil {
				return face
			}
		}
	}

	f, err := opentype.Parse(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: cannot parse font %s: %v\n", path, err)
		return nil
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: cannot create face %s: %v\n", path, err)
		return nil
	}
	return face
}
