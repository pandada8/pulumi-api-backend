import { execFileSync } from 'node:child_process'
import { readFileSync, readdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'

// Include notices for the installed dependency tree as well as copied UI source.
const folders = execFileSync('npm', ['ls', '--all', '--parseable'], {encoding:'utf8'}).trim().split('\n').slice(1)
const notices = ['pulumid console — third-party notices\n', readFileSync('LICENSE-shadcn','utf8')]
for (const folder of [...new Set(folders)].sort()) {
 const pkg = JSON.parse(readFileSync(path.join(folder,'package.json'),'utf8'))
 const files = readdirSync(folder).filter(name=>/^(licen[cs]e|copying|notice)(\.|$)/i.test(name))
 notices.push(`\n${'='.repeat(72)}\n${pkg.name} ${pkg.version} (${typeof pkg.license === 'string' ? pkg.license : 'see license below'})\n`)
 for (const file of files) {
  try {notices.push(readFileSync(path.join(folder,file),'utf8'))} catch { /* Some packages use a license directory. */ }
 }
}
writeFileSync('../internal/httpapi/console/dist/third-party-licenses.txt',notices.join('\n'))
