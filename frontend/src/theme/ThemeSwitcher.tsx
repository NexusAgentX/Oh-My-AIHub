import { Icon, Segmented, type IconName } from '../ui'
import { themeOptions, useThemeMode, type ThemeMode } from './theme'

const icons: Record<ThemeMode, IconName> = { system: 'monitor', light: 'sun', dark: 'moon' }

/** 账户菜单中的主题单选：三个图标按钮，选中后菜单保持打开以便看到效果。 */
export function ThemeMenuGroup() {
  const [mode, setMode] = useThemeMode()
  return (
    <div aria-label="主题" className="theme-menu" role="group">
      <span aria-hidden="true">主题</span>
      <span className="theme-menu-options">
        {themeOptions.map((option) => (
          <button
            aria-checked={mode === option.key}
            aria-label={option.label}
            className="theme-menu-option"
            key={option.key}
            onClick={() => setMode(option.key)}
            role="menuitemradio"
            title={option.label}
            type="button"
          >
            <Icon name={icons[option.key]} size={16} />
          </button>
        ))}
      </span>
    </div>
  )
}

/** 菜单以外的位置（手机“我的”页）：分段选择。 */
export function ThemeSegmented() {
  const [mode, setMode] = useThemeMode()
  return <Segmented label="主题" onChange={setMode} options={themeOptions} value={mode} />
}
