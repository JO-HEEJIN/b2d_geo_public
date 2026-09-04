package meter

import "testing"

func TestNormalizeEndpoint(t *testing.T) {
	cases := map[string]string{
		"/v1/parcel/4145010100100010000": "/v1/parcel/{pnu}",
		"/v1/parcel/by-point":            "/v1/parcel/by-point",
		"/v1/parcel/by-jibun":            "/v1/parcel/by-jibun",
		"/v1/geocode":                    "/v1/geocode",
		"/v1/appraisal/sales":            "/v1/appraisal/sales",
		"/v1/parcel/":                    "/v1/parcel/",
	}
	for in, want := range cases {
		if got := normalizeEndpoint(in); got != want {
			t.Errorf("normalizeEndpoint(%q)=%q want %q", in, got, want)
		}
	}
}
