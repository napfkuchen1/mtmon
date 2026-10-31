// SPDX-License-Identifier: AGPL-3.0-only

// Where the source of the running build lives (AGPL-3.0 §13): the repository tree at the running release tag,
// or the default branch for dev builds.
export const REPO = 'https://github.com/napfkuchen1/mtmon'

export const sourceURL = v => (v && /^\d/.test(v) ? `${REPO}/tree/v${v}` : v && /^v\d/.test(v) ? `${REPO}/tree/${v}` : REPO)
