package enrich

import (
	"strings"
	"sync"
)

// App is a recognised application / online service.
type App struct {
	Key      string `json:"key"`      // stable id, also usable as an i18n/icon key
	Name     string `json:"name"`     // display name
	Category string `json:"category"` // coarse group for filter chips
}

// App categories (English; the UI translates them).
const (
	CatVideo     = "Video"
	CatMusic     = "Music"
	CatSocial    = "Social"
	CatMessaging = "Messaging"
	CatMeetings  = "Meetings"
	CatGaming    = "Gaming"
	CatCloud     = "Cloud & files"
	CatOffice    = "Productivity"
	CatSystem    = "System & updates"
	CatNetwork   = "Network"
	CatSmartHome = "Smart home"
	CatShopping  = "Shopping"
	CatWeb       = "Web & search"
)

type appRule struct {
	App
	hosts []string // domain suffixes (example.com matches example.com and *.example.com)
	orgs  []string // lower-case substrings of the AS organisation (only unambiguous ones)
}

func r(key, name, cat string, hosts []string, orgs ...string) appRule {
	return appRule{App: App{Key: key, Name: name, Category: cat}, hosts: hosts, orgs: orgs}
}

// appRules is the curated table. Principles: only list names that identify the application itself
// (never generic CDN / cloud-hosting domains such as akamai, cloudfront or amazonaws – those say nothing
// about what the device is doing). Longest matching suffix wins, so specific entries such as
// tv.apple.com beat the generic apple.com entry.
var appRules = []appRule{
	// ---- video ----
	r("youtube", "YouTube", CatVideo, []string{"googlevideo.com", "youtube.com", "ytimg.com", "youtu.be", "youtube-nocookie.com", "youtubekids.com", "yt3.ggpht.com"}),
	r("netflix", "Netflix", CatVideo, []string{"netflix.com", "nflxvideo.net", "nflximg.net", "nflxext.com", "nflxso.net"}, "netflix"),
	r("disneyplus", "Disney+", CatVideo, []string{"disneyplus.com", "disney-plus.net", "dssott.com", "bamgrid.com", "disneystreaming.com"}),
	r("primevideo", "Prime Video", CatVideo, []string{"primevideo.com", "aiv-cdn.net", "aiv-delivery.net", "pv-cdn.net", "amazonvideo.com", "atv-ps.amazon.com", "atv-ext.amazon.com"}),
	r("appletv", "Apple TV+", CatVideo, []string{"tv.apple.com", "play.itunes.apple.com", "hls.itunes.apple.com"}),
	r("twitch", "Twitch", CatVideo, []string{"twitch.tv", "ttvnw.net", "jtvnw.net", "twitchcdn.net", "twitchsvc.net"}, "twitch interactive"),
	r("maxhbo", "Max / HBO", CatVideo, []string{"max.com", "hbomax.com", "hbo.com", "hbomaxcdn.com"}),
	r("paramount", "Paramount+", CatVideo, []string{"paramountplus.com", "cbsivideo.com"}),
	r("dazn", "DAZN", CatVideo, []string{"dazn.com", "dazn-api.com", "dazndn.com"}),
	r("joyn", "Joyn", CatVideo, []string{"joyn.de", "joyn.com"}),
	r("rtlplus", "RTL+", CatVideo, []string{"rtlplus.com", "plus.rtl.de", "tvnow.de"}),
	r("mediathek", "ARD / ZDF Mediathek", CatVideo, []string{"ardmediathek.de", "zdf.de", "zdf.fra.sdt.cloud", "ardmediathek.sslcs.cdngc.net"}),
	r("crunchyroll", "Crunchyroll", CatVideo, []string{"crunchyroll.com", "vrv.co"}),
	r("vimeo", "Vimeo", CatVideo, []string{"vimeo.com", "vimeocdn.com"}),
	r("wow", "WOW / Sky", CatVideo, []string{"wowtv.de", "sky.de", "skygo.sky.de"}),
	// ---- music ----
	r("spotify", "Spotify", CatMusic, []string{"spotify.com", "scdn.co", "spotifycdn.com", "spotifycdn.net", "spoti.fi"}, "spotify"),
	r("applemusic", "Apple Music", CatMusic, []string{"music.apple.com", "aod.itunes.apple.com", "aod-ssl.itunes.apple.com"}),
	r("soundcloud", "SoundCloud", CatMusic, []string{"soundcloud.com", "sndcdn.com"}),
	r("deezer", "Deezer", CatMusic, []string{"deezer.com", "dzcdn.net"}),
	r("tidal", "TIDAL", CatMusic, []string{"tidal.com", "tidalhifi.com"}),
	r("sonos", "Sonos", CatMusic, []string{"sonos.com", "ws.sonos.com"}),
	// ---- social ----
	r("instagram", "Instagram", CatSocial, []string{"instagram.com", "cdninstagram.com"}),
	r("facebook", "Facebook", CatSocial, []string{"facebook.com", "fbcdn.net", "fb.com", "fbsbx.com", "facebook.net"}),
	r("meta", "Meta (Facebook / Instagram)", CatSocial, nil, "facebook, inc", "meta platforms"),
	r("threads", "Threads", CatSocial, []string{"threads.net"}),
	r("tiktok", "TikTok", CatSocial, []string{"tiktok.com", "tiktokcdn.com", "tiktokv.com", "musical.ly", "byteoversea.com", "ibytedtos.com", "tiktokcdn-eu.com", "ttwstatic.com"}, "tiktok", "bytedance"),
	r("snapchat", "Snapchat", CatSocial, []string{"snapchat.com", "sc-cdn.net", "snap.com", "snapkit.com"}, "snap inc"),
	r("x", "X (Twitter)", CatSocial, []string{"twitter.com", "twimg.com", "x.com", "t.co"}, "twitter"),
	r("reddit", "Reddit", CatSocial, []string{"reddit.com", "redd.it", "redditmedia.com", "redditstatic.com"}),
	r("linkedin", "LinkedIn", CatSocial, []string{"linkedin.com", "licdn.com"}, "linkedin"),
	r("pinterest", "Pinterest", CatSocial, []string{"pinterest.com", "pinimg.com"}, "pinterest"),
	// ---- messaging ----
	r("whatsapp", "WhatsApp", CatMessaging, []string{"whatsapp.net", "whatsapp.com", "wa.me"}),
	r("telegram", "Telegram", CatMessaging, []string{"telegram.org", "t.me", "telegra.ph", "cdn-telegram.org", "telesco.pe"}, "telegram messenger"),
	r("signal", "Signal", CatMessaging, []string{"signal.org", "whispersystems.org", "signal.art"}),
	r("discord", "Discord", CatMessaging, []string{"discord.com", "discordapp.com", "discord.gg", "discordapp.net", "discord.media", "discordcdn.com"}, "discord inc"),
	r("messenger", "Messenger", CatMessaging, []string{"messenger.com"}),
	r("threema", "Threema", CatMessaging, []string{"threema.ch"}),
	// ---- meetings ----
	r("zoom", "Zoom", CatMeetings, []string{"zoom.us", "zoom.com", "zoomgov.com", "zmtrk.com"}, "zoom video"),
	r("teams", "Microsoft Teams", CatMeetings, []string{"teams.microsoft.com", "teams.live.com", "teams.cloud.microsoft", "skype.com", "lync.com", "skypeforbusiness.com", "teams.events.data.microsoft.com"}),
	r("meet", "Google Meet", CatMeetings, []string{"meet.google.com", "meetings.googleapis.com", "meet.googleapis.com"}),
	r("webex", "Webex", CatMeetings, []string{"webex.com", "wbx2.com", "ciscospark.com"}),
	r("slack", "Slack", CatMeetings, []string{"slack.com", "slack-edge.com", "slack-msgs.com", "slack-core.com"}),
	r("gotomeeting", "GoTo", CatMeetings, []string{"gotomeeting.com", "goto.com", "logmein.com"}),
	// ---- Microsoft ----
	r("onedrive", "OneDrive / SharePoint", CatCloud, []string{"onedrive.com", "onedrive.live.com", "1drv.com", "1drv.ms", "sharepoint.com", "svc.ms", "storage.live.com"}),
	r("m365", "Microsoft 365", CatOffice, []string{"office.com", "office365.com", "office.net", "microsoftonline.com", "microsoftonline-p.com", "outlook.com", "outlook.office.com", "outlook.office365.com", "live.com", "msauth.net", "msftauth.net", "officeapps.live.com", "cdn.office.net", "msocdn.com"}),
	r("windowsupdate", "Windows Update", CatSystem, []string{"windowsupdate.com", "update.microsoft.com", "delivery.mp.microsoft.com", "do.dsp.mp.microsoft.com", "dl.delivery.mp.microsoft.com", "download.microsoft.com", "wustat.windows.com", "ctldl.windowsupdate.com"}),
	r("windowstelemetry", "Windows telemetry", CatSystem, []string{"vortex.data.microsoft.com", "events.data.microsoft.com", "telemetry.microsoft.com", "watson.microsoft.com", "settings-win.data.microsoft.com"}),
	r("msstore", "Microsoft Store", CatSystem, []string{"storeedgefd.dsx.mp.microsoft.com", "displaycatalog.mp.microsoft.com", "licensing.mp.microsoft.com"}),
	// ---- Google ----
	r("gdrive", "Google Drive", CatCloud, []string{"drive.google.com", "docs.google.com", "drive.usercontent.google.com", "sheets.google.com", "docs.googleusercontent.com", "clients6.google.com"}),
	r("gmail", "Gmail", CatOffice, []string{"mail.google.com", "gmail.com", "googlemail.com", "gmail.googleapis.com", "imap.gmail.com", "smtp.gmail.com"}),
	r("gmaps", "Google Maps", CatWeb, []string{"maps.google.com", "maps.googleapis.com", "maps.gstatic.com", "khms.google.com", "khms0.google.com", "khms1.google.com"}),
	r("googleplay", "Google Play", CatSystem, []string{"play.google.com", "play.googleapis.com", "android.clients.google.com", "play-lh.googleusercontent.com", "play-fe.googleapis.com", "android.googleapis.com"}),
	r("chromeupdate", "Chrome / Android updates", CatSystem, []string{"dl.google.com", "update.googleapis.com", "gvt1.com", "gvt2.com", "clients2.google.com", "edgedl.me.gvt1.com"}),
	r("googlefcm", "Google push (FCM)", CatSystem, []string{"mtalk.google.com", "fcm.googleapis.com", "fcm-xmpp.googleapis.com", "firebaseinstallations.googleapis.com"}),
	r("google", "Google services", CatWeb, []string{"google.com", "gstatic.com", "googleapis.com", "googleusercontent.com", "1e100.net", "google.de", "googleadservices.com", "doubleclick.net", "googlesyndication.com", "googletagmanager.com", "google-analytics.com"}),
	// ---- Apple ----
	r("icloud", "iCloud", CatCloud, []string{"icloud.com", "icloud-content.com", "apple-cloudkit.com", "me.com", "mask.icloud.com"}),
	r("appstore", "App Store", CatSystem, []string{"apps.apple.com", "itunes.apple.com", "mzstatic.com", "ppq.apple.com", "buy.itunes.apple.com", "iosapps.itunes.apple.com"}),
	r("appleupdate", "Apple software update", CatSystem, []string{"swcdn.apple.com", "swdist.apple.com", "swscan.apple.com", "mesu.apple.com", "updates.cdn-apple.com", "gdmf.apple.com", "xp.apple.com"}),
	r("applepush", "Apple push (APNs)", CatSystem, []string{"push.apple.com", "push-apple.com.akadns.net"}),
	r("apple", "Apple services", CatSystem, []string{"apple.com", "aaplimg.com", "cdn-apple.com", "apple-dns.net", "apple.news", "ls.apple.com"}, "apple inc", "apple distribution"),
	// ---- gaming ----
	r("steam", "Steam", CatGaming, []string{"steampowered.com", "steamcontent.com", "steamstatic.com", "steamcommunity.com", "steamserver.net", "steamgames.com", "steam-chat.com", "valvesoftware.com", "steamusercontent.com"}, "valve corporation"),
	r("xbox", "Xbox Live", CatGaming, []string{"xboxlive.com", "xbox.com", "xboxservices.com", "xbl.io", "gamepass.com"}),
	r("psn", "PlayStation Network", CatGaming, []string{"playstation.net", "playstation.com", "sonyentertainmentnetwork.com", "ps4.sonyentertainmentnetwork.com", "dl.playstation.net"}, "sony interactive"),
	r("nintendo", "Nintendo", CatGaming, []string{"nintendo.net", "nintendo.com", "nintendowifi.net", "nintendo.co.jp", "nintendo.eu"}, "nintendo"),
	r("epic", "Epic Games", CatGaming, []string{"epicgames.com", "epicgames.dev", "unrealengine.com", "fortnite.com", "epicgames-download1.akamaized.net"}, "epic games"),
	r("battlenet", "Battle.net", CatGaming, []string{"battle.net", "blizzard.com", "battlenet.com.cn"}, "blizzard"),
	r("riot", "Riot Games", CatGaming, []string{"riotgames.com", "leagueoflegends.com", "playvalorant.com", "riotcdn.net"}, "riot games"),
	r("ea", "EA", CatGaming, []string{"ea.com", "origin.com", "easports.com"}, "electronic arts"),
	r("ubisoft", "Ubisoft", CatGaming, []string{"ubisoft.com", "ubi.com", "ubisoftconnect.com"}, "ubisoft"),
	r("roblox", "Roblox", CatGaming, []string{"roblox.com", "rbxcdn.com", "robloxlabs.com"}, "roblox"),
	r("minecraft", "Minecraft", CatGaming, []string{"minecraft.net", "mojang.com", "minecraftservices.com"}),
	r("geforcenow", "GeForce NOW", CatGaming, []string{"nvidiagrid.net", "geforcenow.com"}),
	// ---- cloud storage / productivity ----
	r("dropbox", "Dropbox", CatCloud, []string{"dropbox.com", "dropboxstatic.com", "dropboxapi.com", "dropbox-dns.com", "dropboxusercontent.com"}, "dropbox"),
	r("nextcloud", "Nextcloud", CatCloud, []string{"nextcloud.com"}),
	r("notion", "Notion", CatOffice, []string{"notion.so", "notion.com", "notion-static.com"}),
	r("github", "GitHub", CatOffice, []string{"github.com", "githubusercontent.com", "githubassets.com", "github.io"}, "github"),
	r("adobe", "Adobe Creative Cloud", CatOffice, []string{"adobe.com", "adobe.io", "adobelogin.com", "typekit.net"}, "adobe"),
	// ---- shopping ----
	r("amazon", "Amazon", CatShopping, []string{"amazon.com", "amazon.de", "amazon.co.uk", "media-amazon.com", "ssl-images-amazon.com", "amazon-adsystem.com"}),
	r("alexa", "Amazon Alexa", CatSmartHome, []string{"alexa.amazon.com", "avs-alexa-na.amazon.com", "avs-alexa-eu.amazon.com", "device-metrics-us.amazon.com", "device-metrics-us-2.amazon.com", "alexa.amazon.de"}),
	r("ebay", "eBay", CatShopping, []string{"ebay.com", "ebay.de", "ebayimg.com", "ebaystatic.com"}),
	r("zalando", "Zalando", CatShopping, []string{"zalando.de", "zalando.com", "ztat.net"}),
	// ---- network / VPN / DNS / time ----
	r("warp", "Cloudflare WARP", CatNetwork, []string{"cloudflareclient.com", "warp.plus", "cloudflare-gateway.com"}),
	r("cfdns", "Cloudflare DNS", CatNetwork, []string{"one.one.one.one", "cloudflare-dns.com", "dns.cloudflare.com", "mozilla.cloudflare-dns.com"}),
	r("tailscale", "Tailscale", CatNetwork, []string{"tailscale.com", "tailscale.io", "ts.net"}, "tailscale"),
	r("pihole", "Pi-hole / AdGuard DNS", CatNetwork, []string{"pi-hole.net", "adguard.com", "adguard-dns.com", "adguard-dns.io", "dns.adguard.com"}),
	r("quad9", "Quad9 DNS", CatNetwork, []string{"quad9.net"}),
	r("ntp", "NTP time sync", CatNetwork, []string{"ntp.org", "pool.ntp.org", "time.windows.com", "time.apple.com", "time.google.com", "time.cloudflare.com", "ntp.ubuntu.com", "ptb.de"}),
	r("nordvpn", "NordVPN", CatNetwork, []string{"nordvpn.com", "nordcdn.com", "nordvpn.net"}),
	r("protonvpn", "Proton VPN / Mail", CatNetwork, []string{"proton.me", "protonvpn.com", "protonmail.com", "protonmail.ch", "proton.ch"}),
	r("speedtest", "Speedtest", CatNetwork, []string{"speedtest.net", "ookla.com", "fast.com"}),
	r("teamviewer", "TeamViewer", CatNetwork, []string{"teamviewer.com"}),
	r("anydesk", "AnyDesk", CatNetwork, []string{"anydesk.com"}),
	// ---- smart home / IoT ----
	r("nabucasa", "Home Assistant Cloud", CatSmartHome, []string{"nabucasa.com", "ui.nabu.casa", "nabu.casa", "home-assistant.io"}),
	r("plex", "Plex", CatVideo, []string{"plex.tv", "plex.direct", "plexapp.com"}, "plex"),
	r("hue", "Philips Hue", CatSmartHome, []string{"meethue.com", "philips-hue.com"}),
	r("tuya", "Tuya / Smart Life", CatSmartHome, []string{"tuya.com", "tuyaeu.com", "tuyaus.com", "iot-tuya.com"}),
	r("shelly", "Shelly Cloud", CatSmartHome, []string{"shelly.cloud"}),
	r("ring", "Ring / Blink", CatSmartHome, []string{"ring.com", "blinkforhome.com"}),
	r("tado", "tado°", CatSmartHome, []string{"tado.com"}),
	r("homekit", "Apple Home / HomeKit", CatSmartHome, []string{"home.apple.com"}),
	r("bambu", "Bambu Cloud", CatSmartHome, []string{"bambulab.com"}),
	r("mikrotik", "MikroTik", CatNetwork, []string{"mikrotik.com", "cloud2.mikrotik.com"}),
}

// portRules recognise traffic by well-defined UDP ports; they win over names because the port says
// what the traffic is, whereas a name only says who it talks to.
var portRules = map[[2]uint16]App{
	{17, 123}:   {Key: "ntp", Name: "NTP time sync", Category: CatNetwork},
	{17, 51820}: {Key: "wireguard", Name: "WireGuard VPN", Category: CatNetwork},
	{17, 41641}: {Key: "tailscale", Name: "Tailscale", Category: CatNetwork},
}

type appIndex struct {
	byHost map[string]*appRule
	orgs   []orgRule
}

type orgRule struct {
	sub string
	r   *appRule
}

var (
	idxOnce sync.Once
	idx     appIndex
)

func buildIndex() {
	idx.byHost = map[string]*appRule{}
	for i := range appRules {
		ar := &appRules[i]
		for _, h := range ar.hosts {
			if _, dup := idx.byHost[h]; !dup { // first entry wins on duplicates
				idx.byHost[h] = ar
			}
		}
		for _, o := range ar.orgs {
			idx.orgs = append(idx.orgs, orgRule{sub: o, r: ar})
		}
	}
}

// Apps returns the curated application table (read-only copy of the metadata).
func Apps() []App {
	out := make([]App, len(appRules))
	for i, a := range appRules {
		out[i] = a.App
	}
	return out
}

// AppCategories returns the distinct categories in table order.
func AppCategories() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, a := range appRules {
		if !seen[a.Category] {
			seen[a.Category] = true
			out = append(out, a.Category)
		}
	}
	return out
}

// ClassifyApp maps a destination to an application. host is a DNS / reverse-DNS name (may be empty),
// org the AS organisation (may be empty). It returns ok=false when nothing is recognised – callers must
// then show nothing rather than guess. Order: well-known UDP port → hostname suffix (most specific
// first) → unambiguous AS organisation.
func ClassifyApp(host, org string, proto uint8, rport uint16) (App, bool) {
	idxOnce.Do(buildIndex)
	if a, ok := portRules[[2]uint16{uint16(proto), rport}]; ok {
		return a, true
	}
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	for h := host; h != ""; {
		if ar, ok := idx.byHost[h]; ok {
			return ar.App, true
		}
		i := strings.IndexByte(h, '.')
		if i < 0 {
			break
		}
		h = h[i+1:]
	}
	if org = strings.ToLower(strings.TrimSpace(org)); org != "" {
		for _, o := range idx.orgs {
			if strings.Contains(org, o.sub) {
				return o.r.App, true
			}
		}
	}
	return App{}, false
}
