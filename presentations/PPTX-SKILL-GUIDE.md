# PPTX Skill 使用指南

## 前置准备

### 1. Node.js 依赖 (pnpm)

```bash
cd <your-workspace>
pnpm init
pnpm add pptxgenjs playwright sharp
```

### 2. Python 依赖 (uv)

```bash
cd <your-workspace>
uv venv .venv
source .venv/bin/activate
uv pip install python-pptx Pillow
```

> 注: Python 环境主要用于缩略图生成和模板编辑功能，如果只是从 HTML 创建 PPT 则不需要。

### 3. 可选: LibreOffice (用于生成缩略图预览)

```bash
sudo apt-get install libreoffice poppler-utils
```

---

## 工作流程

### 从零创建 PPT (html2pptx)

1. **创建 HTML 幻灯片文件**
   - 尺寸: 16:9 使用 `width: 720pt; height: 405pt`
   - 所有文本必须在 `<p>`, `<h1>`-`<h6>`, `<ul>`, `<ol>` 标签内
   - 只用 web-safe 字体: Arial, Helvetica, Verdana 等
   - 不要用 CSS 渐变，需要渐变请用 Sharp 预先生成 PNG

2. **编写转换脚本**

```javascript
const pptxgen = require('pptxgenjs');
const html2pptx = require('<skill-path>/scripts/html2pptx');

async function createPresentation() {
    const pptx = new pptxgen();
    pptx.layout = 'LAYOUT_16x9';

    await html2pptx('slide1.html', pptx);
    await html2pptx('slide2.html', pptx);
    // ...

    await pptx.writeFile({ fileName: 'output.pptx' });
}

createPresentation().catch(console.error);
```

3. **运行生成**

```bash
node create-pptx.js
```

---

## 常见问题

| 问题 | 解决方案 |
|------|----------|
| HTML content overflows body | 减小字体大小、padding、margin，确保底部留 0.5" 边距 |
| Text will NOT appear | 确保文本在 `<p>`, `<h1>`-`<h6>`, `<ul>`, `<ol>` 标签内 |
| 颜色不生效 | PptxGenJS 中使用 `"FF0000"` 而非 `"#FF0000"` |
| 渐变不显示 | 用 Sharp 将 SVG 渐变转为 PNG 图片 |

---

## 目录结构参考

```
workspace/
├── .venv/              # Python 虚拟环境
├── node_modules/       # Node 依赖
├── slides/             # HTML 幻灯片源文件
│   ├── slide01.html
│   └── slide02.html
├── create-pptx.js      # 转换脚本
├── package.json
└── output.pptx         # 生成的 PPT
```
