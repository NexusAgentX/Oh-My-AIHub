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

export const unitPriceLabels: Record<string, string> = {
  web_search: '网页搜索 / 次',
  file_search: '文件搜索 / 次',
  google_search_query: 'Google 搜索 / 查询',
  google_search_prompt: 'Google 搜索 / 请求',
  google_maps_query: 'Google 地图 / 查询',
  google_maps_prompt: 'Google 地图 / 请求',
  container_1gb: '容器 1 GB / 会话',
  container_4gb: '容器 4 GB / 会话',
  container_16gb: '容器 16 GB / 会话',
  container_64gb: '容器 64 GB / 会话',
  code_execution_second: '代码执行 / 秒',
  storage_gb_hour: '存储 / GB·小时',
}
