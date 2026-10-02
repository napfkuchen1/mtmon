import shell from './ui_shell.js'
import pagesA from './ui_pages_a.js'
import pagesB from './ui_pages_b.js'
import server from './server.js'
// key = English source string, value = German. Keys with {name} placeholders also match backend text via tr().
export const de = { ...shell, ...pagesA, ...pagesB, ...server }
