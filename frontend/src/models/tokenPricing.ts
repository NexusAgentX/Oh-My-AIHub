export const tokenPriceLabels: Record<string, string> = {
  cache_write_5m: '缓存写 5 分钟',
  cache_write_1h: '缓存写 1 小时',
  ...Object.fromEntries(
    ['input', 'output', 'cache_read'].flatMap((bucket, index) =>
      ['text', 'image', 'audio', 'video'].map((mod, j) => [
        `${bucket}_${mod}`,
        `${['输入', '输出', '缓存读'][index]} · ${['文本', '图片', '音频', '视频'][j]}`,
      ]),
    ),
  ),
}
