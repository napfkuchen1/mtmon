package enrich

import "testing"

func TestClassifyApp(t *testing.T) {
	cases := []struct {
		name, host, org string
		proto           uint8
		port            uint16
		want            string // app key, "" = not recognised
	}{
		{"youtube video cdn", "rr3---sn-4g5e6nsz.googlevideo.com", "", 6, 443, "youtube"},
		{"youtube root", "youtube.com", "", 6, 443, "youtube"},
		{"youtube trailing dot + case", "WWW.YouTube.com.", "", 6, 443, "youtube"},
		{"ytimg", "i.ytimg.com", "", 6, 443, "youtube"},
		{"spotify", "audio-ak-spotify-com.akamaized.net", "", 6, 443, ""}, // CDN name: honest, not guessed
		{"spotify cdn", "i.scdn.co", "", 6, 443, "spotify"},
		{"netflix", "ipv4-c001-fra001-ix.1.oca.nflxvideo.net", "", 6, 443, "netflix"},
		{"netflix by org", "", "NETFLIX-ASN", 6, 443, "netflix"},
		{"specific beats generic apple", "tv.apple.com", "", 6, 443, "appletv"},
		{"apple generic", "gsp-ssl.ls.apple.com", "", 6, 443, "apple"},
		{"icloud", "p45-content.icloud-content.com", "", 6, 443, "icloud"},
		{"teams beats microsoft", "teams.microsoft.com", "", 6, 443, "teams"},
		{"windows update", "dl.delivery.mp.microsoft.com", "", 6, 80, "windowsupdate"},
		{"google drive vs generic google", "drive.google.com", "", 6, 443, "gdrive"},
		{"google generic", "www.google.com", "", 6, 443, "google"},
		{"rdns 1e100", "fra16s48-in-f14.1e100.net", "", 6, 443, "google"},
		{"whatsapp", "mmg.whatsapp.net", "", 6, 443, "whatsapp"},
		{"tiktok", "v16m.tiktokcdn.com", "", 6, 443, "tiktok"},
		{"discord", "gateway.discord.gg", "", 6, 443, "discord"},
		{"twitch", "video-edge-1.fra02.hls.ttvnw.net", "", 6, 443, "twitch"},
		{"steam", "cache1-fra1.steamcontent.com", "", 6, 443, "steam"},
		{"steam by org", "", "Valve Corporation", 17, 27015, "steam"},
		{"xbox", "assets1.xboxlive.com", "", 6, 443, "xbox"},
		{"psn", "gs2.ww.prod.dl.playstation.net", "", 6, 443, "psn"},
		{"nintendo", "ctest.cdn.nintendo.net", "", 6, 443, "nintendo"},
		{"epic", "download.epicgames.com", "", 6, 443, "epic"},
		{"zoom", "us02web.zoom.us", "", 17, 8801, "zoom"},
		{"dropbox", "client.dropbox.com", "", 6, 443, "dropbox"},
		{"warp", "engage.cloudflareclient.com", "", 17, 2408, "warp"},
		{"tailscale host", "derp5.tailscale.com", "", 6, 443, "tailscale"},
		{"tailscale port", "", "", 17, 41641, "tailscale"},
		{"wireguard port", "", "", 17, 51820, "wireguard"},
		{"ntp port beats host", "time.apple.com", "", 17, 123, "ntp"},
		{"ntp host", "0.pool.ntp.org", "", 6, 443, "ntp"},
		{"nabu casa", "abc123.ui.nabu.casa", "", 6, 443, "nabucasa"},
		{"plex", "1-2-3-4.abcdef.plex.direct", "", 6, 32400, "plex"},
		{"pihole", "dns.adguard.com", "", 6, 443, "pihole"},
		{"suffix must be a label boundary", "notyoutube.com", "", 6, 443, ""},
		{"lookalike suffix", "youtube.com.evil.example", "", 6, 443, ""},
		{"unknown host", "example.org", "Some Hosting Ltd", 6, 443, ""},
		{"empty", "", "", 6, 443, ""},
		{"cdn is not an app", "d111111abcdef8.cloudfront.net", "Amazon.com, Inc.", 6, 443, ""},
		{"ambiguous org (cloudflare) not guessed", "", "CLOUDFLARENET", 6, 443, ""},
		{"meta by org", "", "Facebook, Inc.", 6, 443, "meta"},
		{"host beats org", "i.scdn.co", "Facebook, Inc.", 6, 443, "spotify"},
	}
	for _, c := range cases {
		a, ok := ClassifyApp(c.host, c.org, c.proto, c.port)
		if c.want == "" {
			if ok {
				t.Errorf("%s: want no match, got %q", c.name, a.Key)
			}
			continue
		}
		if !ok || a.Key != c.want {
			t.Errorf("%s: want %q, got %q (ok=%v)", c.name, c.want, a.Key, ok)
		}
	}
}

func TestAppTableSanity(t *testing.T) {
	if n := len(Apps()); n < 60 {
		t.Fatalf("only %d apps in table", n)
	}
	keys := map[string]bool{}
	for _, a := range appRules {
		if a.Key == "" || a.Name == "" || a.Category == "" {
			t.Errorf("incomplete entry %+v", a.App)
		}
		if keys[a.Key] {
			t.Errorf("duplicate key %s", a.Key)
		}
		keys[a.Key] = true
		for _, h := range a.hosts {
			if h != toLowerTrim(h) || len(h) < 4 {
				t.Errorf("%s: bad host pattern %q", a.Key, h)
			}
		}
	}
	// a pattern listed twice would silently make the first entry win
	seen := map[string]string{}
	for _, a := range appRules {
		for _, h := range a.hosts {
			if o, dup := seen[h]; dup && o != a.Key {
				t.Errorf("host %s listed for %s and %s", h, o, a.Key)
			}
			seen[h] = a.Key
		}
	}
}

func toLowerTrim(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
