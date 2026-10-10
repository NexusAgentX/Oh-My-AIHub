// 首屏绘制前按本机偏好设置 data-theme，深色用户不会先看到一闪而过的浅色页面。
// CSP 不允许内联脚本，所以是独立文件；键名、取值与解析规则须与 src/theme/theme.ts 一致（有测试校验）。
;(function () {
  var mode = null
  try {
    mode = window.localStorage.getItem('oma-theme')
  } catch {
    // 无法读取本机存储时跟随系统
  }
  var dark =
    mode === 'dark' ||
    (mode !== 'light' &&
      typeof window.matchMedia === 'function' &&
      window.matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.setAttribute('data-theme', dark ? 'dark' : 'light')
})()
