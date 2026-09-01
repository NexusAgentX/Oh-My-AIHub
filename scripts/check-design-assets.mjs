import { readFile, readdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const sourcesDirectory = path.join(repositoryRoot, "design", "sources");
const previewsDirectory = path.join(repositoryRoot, "design", "previews");
const errors = [];

async function listFiles(directory, extension) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];

  for (const entry of entries) {
    const entryPath = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...await listFiles(entryPath, extension));
    } else if (entry.isFile() && entry.name.endsWith(extension)) {
      files.push(entryPath);
    }
  }

  return files.sort();
}

function relative(filePath) {
  return path.relative(repositoryRoot, filePath);
}

function validateStableName(filePath) {
  const stem = path.basename(filePath, path.extname(filePath));
  if (!/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(stem)) {
    errors.push(`${relative(filePath)}: 文件名必须使用小写 kebab-case`);
  }
  if (/(?:^|-)(?:v\d+|final|new)(?:-|$)/i.test(stem) || /\d{4}-\d{2}-\d{2}/.test(stem)) {
    errors.push(`${relative(filePath)}: 文件名不得包含人工版本或日期后缀`);
  }
  return stem;
}

function validatePortableContent(filePath, source) {
  const forbidden = [
    { pattern: /\/(?:Users|home|root|private\/var\/folders)\//, label: "本机绝对路径" },
    { pattern: /[A-Za-z]:\\Users\\/, label: "Windows 用户绝对路径" },
    { pattern: /-----BEGIN [A-Z ]*PRIVATE KEY-----/, label: "私钥" },
    { pattern: /\bsk-[A-Za-z0-9_-]{16,}\b/, label: "API Key" },
    { pattern: /\bgh[pousr]_[A-Za-z0-9]{20,}\b/, label: "GitHub Token" },
    { pattern: /\bAKIA[0-9A-Z]{16}\b/, label: "AWS Access Key" },
    { pattern: /\bBearer\s+[A-Za-z0-9._~-]{16,}\b/i, label: "Bearer Token" }
  ];

  for (const { pattern, label } of forbidden) {
    if (pattern.test(source)) {
      errors.push(`${relative(filePath)}: 检测到${label}`);
    }
  }
}

function hasEditableNodes(document) {
  if (Array.isArray(document.children) && document.children.length > 0) {
    return true;
  }

  return Array.isArray(document.pages)
    && document.pages.some((page) => Array.isArray(page?.children) && page.children.length > 0);
}

async function validatePng(filePath) {
  const data = await readFile(filePath);
  const signature = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
  if (data.length < 24 || !data.subarray(0, 8).equals(signature)) {
    errors.push(`${relative(filePath)}: 不是有效 PNG`);
    return;
  }

  const width = data.readUInt32BE(16);
  const height = data.readUInt32BE(20);
  if (width === 0 || height === 0) {
    errors.push(`${relative(filePath)}: PNG 尺寸无效`);
  }
}

const sourceFiles = await listFiles(sourcesDirectory, ".op");
const previewFiles = await listFiles(previewsDirectory, ".png");

if (sourceFiles.length === 0) {
  errors.push("design/sources: 至少需要一个 .op 设计源文件");
}

const sourceStems = new Set();
for (const sourceFile of sourceFiles) {
  const stem = validateStableName(sourceFile);
  sourceStems.add(stem);
  const source = await readFile(sourceFile, "utf8");
  validatePortableContent(sourceFile, source);

  let document;
  try {
    document = JSON.parse(source);
  } catch (error) {
    errors.push(`${relative(sourceFile)}: JSON 解析失败（${error.message}）`);
    continue;
  }

  if (typeof document.version !== "string" || document.version.length === 0) {
    errors.push(`${relative(sourceFile)}: 缺少 OpenPencil version`);
  }
  if (!hasEditableNodes(document)) {
    errors.push(`${relative(sourceFile)}: 缺少可编辑设计节点`);
  }

  const previewFile = path.join(previewsDirectory, `${stem}.png`);
  try {
    await validatePng(previewFile);
  } catch (error) {
    if (error.code === "ENOENT") {
      errors.push(`${relative(sourceFile)}: 缺少同名预览 design/previews/${stem}.png`);
    } else {
      throw error;
    }
  }
}

for (const previewFile of previewFiles) {
  const stem = validateStableName(previewFile);
  if (!sourceStems.has(stem)) {
    errors.push(`${relative(previewFile)}: 没有对应的 design/sources/${stem}.op`);
  }
}

if (errors.length > 0) {
  console.error(errors.map((error) => `- ${error}`).join("\n"));
  process.exitCode = 1;
} else {
  console.log(`设计资产检查通过：${sourceFiles.length} 个源文件，${previewFiles.length} 个预览。`);
}
