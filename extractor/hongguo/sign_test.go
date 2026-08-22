package hongguo

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func vectorBody() []byte {
	v := map[string]any{"biz_param": map[string]any{"detail_page_version": 0, "device_level": 3, "disable_digg_stat": false, "need_all_video_definition": true, "need_mp4_align": false, "use_os_player": false, "use_server_dns": false, "video_platform": 1024}, "mixed_video_id_map": map[string]any{"1004": []string{"7671229514868853784"}}}
	b, _ := json.Marshal(v)
	return b
}
func TestGorgonCompactBody(t *testing.T) {
	q := "iid=6745102938476510293&device_id=5938271046582391746&ac=wifi&channel=update_64&aid=8662&app_name=novelread&version_code=71332&version_name=7.1.3.32&device_platform=android&os=android&ssmix=a&device_type=25053RT47C&device_brand=Redmi&language=zh&os_api=36&os_version=16&manifest_version_code=71332&resolution=1280*2772&dpi=520&update_version_code=71332&host_abi=arm64-v8a&dragon_device_type=phone&pv_player=71332&compliance_status=0&need_personal_recommend=1&player_so_load=1&is_android_pad_screen=0&ts=1700000000&_rticket=1700000000123"
	if got := encryptGorgon(vectorBody(), q, 1700000000, 0x1234); got != "840434124001387d3bb6379450581364e71df6105a46126993f6" {
		t.Fatal(got)
	}
}

func TestGorgonIntermediate(t *testing.T) {
	q := "iid=6745102938476510293&device_id=5938271046582391746&ac=wifi&channel=update_64&aid=8662&app_name=novelread&version_code=71332&version_name=7.1.3.32&device_platform=android&os=android&ssmix=a&device_type=25053RT47C&device_brand=Redmi&language=zh&os_api=36&os_version=16&manifest_version_code=71332&resolution=1280*2772&dpi=520&update_version_code=71332&host_abi=arm64-v8a&dragon_device_type=phone&pv_player=71332&compliance_status=0&need_personal_recommend=1&player_so_load=1&is_android_pad_screen=0&ts=1700000000&_rticket=1700000000123"
	qm := md5.Sum([]byte(q))
	bm := md5.Sum(pythonJSONForHash(vectorBody()))
	t.Logf("qm=%x bm=%x", qm, bm)
	data := append([]byte{}, qm[:4]...)
	data = append(data, bm[:4]...)
	data = append(data, 0, 0, 0, 0)
	data = append(data, uint32LE(67503104)...)
	data = append(data, uint32BE(1700000000)...)
	t.Logf("data=%x rc=%x", data, rc4Gorgon(data, []byte{0x4a, 0x40, 0x16, 0x12, 0x47, 0x6c, 0x01, 0x34}))
	if got := encryptGorgon(vectorBody(), q, 1700000000, 0x1234); got != "840434124001387d3bb6379450581364e71df6105a46126993f6" {
		t.Fatal(got)
	}
}

func TestSigningVectors(t *testing.T) {
	q := parseOrderedQuery("iid=6745102938476510293&device_id=5938271046582391746&ac=wifi&channel=update_64&aid=8662&app_name=novelread&version_code=71332&version_name=7.1.3.32&device_platform=android&os=android&ssmix=a&device_type=25053RT47C&device_brand=Redmi&language=zh&os_api=36&os_version=16&manifest_version_code=71332&resolution=1280*2772&dpi=520&update_version_code=71332&host_abi=arm64-v8a&dragon_device_type=phone&pv_player=71332&compliance_status=0&need_personal_recommend=1&player_so_load=1&is_android_pad_screen=0")
	sr := signRandom{Gorgon: 0x1234, Helios: 0x23456789, Hash: 0x456789ab, XM: 0x56789abc, Proto: 0x3456789a, Launch: 111, PID: 11001}
	_, h, err := signRequest("https://api5-normal-sinfonlineb.fqnovel.com/novel/player/multi_video_model/v1/", q, vectorBody(), signerDevice{"5938271046582391746", "6745102938476510293"}, time.Unix(1700000000, 123000000), sr)
	if err != nil {
		t.Fatal(err)
	}
	wants := map[string]string{"x-gorgon": "840434124001387d3bb6379450581364e71df6105a46126993f6", "x-helios": "iWdFIxVOAi2lWL2Pa8BT7Vgsm7xOTuxagm2MZPVyM67ZQ4lP", "x-ladon": "ZVPxAA==", "x-argus": "APFTZQ==", "x-ss-stub": "6BC14A9880A937ED310F0017AE2D308F"}
	for k, w := range wants {
		if h[k] != w {
			t.Errorf("%s=%s want %s", k, h[k], w)
		}
	}
	medusa, err := base64.StdEncoding.DecodeString(h["x-medusa"])
	if err != nil {
		t.Fatalf("x-medusa is not base64: %v", err)
	}
	if len(medusa) < 100 || medusa[0] != 3 {
		t.Fatalf("x-medusa has invalid envelope: len=%d prefix=%x", len(medusa), medusa[:1])
	}
	_, h2, err := signRequest("https://api5-normal-sinfonlineb.fqnovel.com/novel/player/multi_video_model/v1/", q, vectorBody(), signerDevice{"5938271046582391746", "6745102938476510293"}, time.Unix(1700000000, 123000000), sr)
	if err != nil || h2["x-medusa"] != h["x-medusa"] {
		t.Fatal("x-medusa is not deterministic with all inputs controlled")
	}
}
