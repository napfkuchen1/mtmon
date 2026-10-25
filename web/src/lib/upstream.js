// Texts for the ISP modem / router in front of a gateway (banner and topology node).
import { t } from './i18n.svelte.js'
export const modeText = m => (m.startsWith('transfer net ') ? t('transfer net {net}', { net: m.slice(13) }) : m === 'double NAT' ? t('double NAT') : m === 'tunnel' ? t('tunnel') : m)
export const labelText = u => (u.no_ip ? t('ONT / modem (no IP)') : u.vendor ? t('Modem / router ({vendor})', { vendor: u.label.replace(/^Modem \/ router \(/, '').replace(/\)$/, '') }) : t('Modem / router'))
