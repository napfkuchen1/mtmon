import shell from './ui_shell.js'
import pagesA from './ui_pages_a.js'
import pagesB from './ui_pages_b.js'
import server from './server.js'
import v07server from './v07_server.js'
import v07updates from './v07_updates.js'
import v07clients from './v07_clients.js'
import v07advice from './v07_advice.js'
import v08ui from './v08_ui.js'
// key = English source string, value = German. Keys with {name} placeholders also match backend text via tr().
export const de = { ...shell, ...pagesA, ...pagesB, ...server, ...v07updates, ...v07clients, ...v07advice, ...v07server, ...v08ui }
