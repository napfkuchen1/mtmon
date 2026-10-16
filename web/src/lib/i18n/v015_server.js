// v0.15.1: texts produced by the backend (alerts, suggestions) – review iteration 3
export default {
  "Blocked packets arrive with your own public address {ip} as sender": "Blockierte Pakete kommen mit Ihrer eigenen öffentlichen Adresse {ip} als Absender an",
  "mtmon cannot reach {n} devices: {names} – several devices failing together point to a shared cause (the link between mtmon and the network, a switch or power), not to each device": "mtmon erreicht {n} Geräte nicht: {names} – mehrere gleichzeitige Ausfälle deuten auf eine gemeinsame Ursache (Verbindung zwischen mtmon und Netz, Switch oder Strom), nicht auf jedes Gerät einzeln",
  "mtmon cannot reach {n} devices: {names} – all answer but refuse the API connection: check the API service / firewall on the devices": "mtmon erreicht {n} Geräte nicht: {names} – alle antworten, lehnen aber die API-Verbindung ab: API-Dienst bzw. Firewall der Geräte prüfen",
  "{dev} unreachable: {err} (timeout)": "{dev} nicht erreichbar: {err} (Zeitüberschreitung)",
  "{dev} unreachable: {err} (refused)": "{dev} nicht erreichbar: {err} (Verbindung abgelehnt)",
  " – one known client causes the load, probably a download, backup or stream": " – ein bekannter Client verursacht die Last, vermutlich Download, Backup oder Stream",
  "WAN link on {dev} went down ({iface}) – recurring: the same drop happened on {n} earlier days around {time}, probably scheduled (provider reconnect, router scheduler)": "WAN-Verbindung auf {dev} ist ausgefallen ({iface}) – wiederkehrend: derselbe Ausfall trat an {n} früheren Tagen gegen {time} auf, vermutlich geplant (Provider-Neuverbindung, Router-Scheduler)",
}
