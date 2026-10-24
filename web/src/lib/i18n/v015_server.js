// v0.15.1: texts produced by the backend (alerts, suggestions) – review iteration 3
export default {
  "Blocked packets arrive with your own public address {ip} as sender": "Blockierte Pakete kommen mit Ihrer eigenen öffentlichen Adresse {ip} als Absender an",
  "mtmon cannot reach {n} devices: {names} – several devices failing together point to a shared cause (the link between mtmon and the network, a switch or power), not to each device": "mtmon erreicht {n} Geräte nicht: {names} – mehrere gleichzeitige Ausfälle deuten auf eine gemeinsame Ursache (Verbindung zwischen mtmon und Netz, Switch oder Strom), nicht auf jedes Gerät einzeln",
  "mtmon cannot reach {n} devices: {names} – all answer but refuse the API connection: check the API service / firewall on the devices": "mtmon erreicht {n} Geräte nicht: {names} – alle antworten, lehnen aber die API-Verbindung ab: API-Dienst bzw. Firewall der Geräte prüfen",
  "{dev} unreachable: {err} (timeout)": "{dev} nicht erreichbar: {err} (Zeitüberschreitung)",
  "{dev} unreachable: {err} (refused)": "{dev} nicht erreichbar: {err} (Verbindung abgelehnt)",
  " – one known client causes the load, probably a download, backup or stream": " – ein bekannter Client verursacht die Last, vermutlich Download, Backup oder Stream",
  "Traffic Flow goes via UDP 2055, firewall syslog via UDP 5514 from the router to mtmon. The router sends them itself (chain output), so a rule is only needed on a router in between, when mtmon is in a different network.": "Traffic Flow geht per UDP 2055, Firewall-Syslog per UDP 5514 vom Router zu mtmon. Der Router sendet beides selbst (Chain output); eine Regel ist nur auf einem Router dazwischen nötig, wenn mtmon in einem anderen Netz liegt.",
  "An isolation rule on {dev} blocks traffic you cannot see": "Eine Isolationsregel auf {dev} blockt Traffic, den du nicht siehst",
  "{n} isolation rule(s) are matching packets right now but are not logged: you cannot tell who tries to cross the segment": "{n} Isolationsregel(n) greifen gerade, sind aber nicht geloggt: du siehst nicht, wer das Segment verlassen will",
  "Source: router rules read {time}": "Quelle: Router-Regeln gelesen {time}",
  "Source: router rules read at an unknown time (older stored probe, refreshed hourly)": "Quelle: Router-Regeln zu unbekannter Zeit gelesen (älterer gespeicherter Abruf, stündlich erneuert)",
  "An enabled FastTrack rule exists on {dev} (router rules read {time})": "Auf {dev} ist eine aktive FastTrack-Regel vorhanden (Router-Regeln gelesen {time})",
  "An enabled FastTrack rule exists on {dev} (router rules read at an unknown time (older stored probe, refreshed hourly))": "Auf {dev} ist eine aktive FastTrack-Regel vorhanden (Router-Regeln zu unbekannter Zeit gelesen)",
  "Based on the rules stored at the last router probe (hourly, and right after setup or a feature change); router rules read {time}.": "Beruht auf den Regeln des letzten Router-Abrufs (stündlich sowie direkt nach Einrichtung oder Funktionsänderung); Router-Regeln gelesen {time}.",
  "WAN link on {dev} went down ({iface}) – recurring: the same drop happened on {n} earlier days around {time}, probably scheduled (provider reconnect, router scheduler)": "WAN-Verbindung auf {dev} ist ausgefallen ({iface}) – wiederkehrend: derselbe Ausfall trat an {n} früheren Tagen gegen {time} auf, vermutlich geplant (Provider-Neuverbindung, Router-Scheduler)",
}
