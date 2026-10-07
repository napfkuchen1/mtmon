// German translations (key = English source text) – 0.13.0 UI additions
export default {
  "not sent (normal for a plain page load)": "nicht gesendet (normal beim einfachen Seitenaufruf)",
  "Address in this browser": "Adresse in diesem Browser",
  "Fix automatically": "Automatisch beheben",
  "Fixed": "Behoben",
  "Log allowed traffic (which rule let it through)": "Erlaubten Verkehr loggen (welche Regel hat ihn durchgelassen)",
  "Turns on logging for the accept rules you pick, so the Firewall page also shows what was allowed and by which rule. Needs the firewall log target; it is set up with it if missing. Rules that match every packet of a connection are noisy: pick with care.": "Schaltet das Logging für die gewählten Accept-Regeln ein, sodass die Firewall-Seite auch zeigt, was erlaubt wurde und durch welche Regel. Braucht das Firewall-Log-Ziel; es wird bei Bedarf mit eingerichtet. Regeln, die auf jedes Paket einer Verbindung passen, sind laut: mit Bedacht wählen.",
  "Log every new connection (proof of 'allowed')": "Jede neue Verbindung loggen (Nachweis „erlaubt“)",
  "Adds one rule at the end of the forward chain that logs each new connection that passed all drop rules. Complete picture of allowed connections, but more router CPU and log volume.": "Fügt am Ende der Forward-Chain eine Regel hinzu, die jede neue Verbindung loggt, die alle Drop-Regeln passiert hat. Vollständiges Bild der erlaubten Verbindungen, aber mehr Router-CPU und Logvolumen.",
  "Log on these allow rules:": "Logging auf diesen Allow-Regeln:",
  "noisy": "laut",
  "already logs": "loggt bereits",
  "No allow rules found.": "Keine Allow-Regeln gefunden.",
  "The firewall log target is set up as well if it is missing. “Noisy” rules match every packet of established connections.": "Das Firewall-Log-Ziel wird bei Bedarf mit eingerichtet. „Laute“ Regeln passen auf jedes Paket bestehender Verbindungen.",
}
