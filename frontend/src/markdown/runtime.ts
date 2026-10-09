import luteURL from 'vditor/dist/js/lute/lute.min.js?url&no-inline'
import localeURL from 'vditor/dist/js/i18n/zh_CN.js?url&no-inline'
import iconsURL from 'vditor/dist/js/icons/ant.js?url&no-inline'

let runtime: Promise<void> | undefined
function script(url: string, id: string) {
  return new Promise<void>((resolve, reject) => {
    if (document.getElementById(id)) return resolve()
    const element = document.createElement('script')
    element.src = url
    element.onload = () => { element.id = id; resolve() }
    element.onerror = () => { element.remove(); reject(new Error('编辑器资源加载失败，请刷新重试')) }
    document.head.append(element)
  })
}

// Vditor cannot safely be destroyed during its asynchronous resource loading.
// Resolve all prerequisites before constructing an instance in a React effect.
export function loadMarkdownRuntime() {
  runtime ??= Promise.all([
    script(luteURL, 'vditorLuteScript'),
    script(localeURL, 'aihubMarkdownLocale'),
    script(iconsURL, 'aihubMarkdownIcons'),
  ]).then(() => undefined).catch((error) => { runtime = undefined; throw error })
  return runtime
}
