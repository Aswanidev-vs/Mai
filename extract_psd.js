const PSD = require('psd');
const fs = require('fs');
const path = require('path');

const psdPath = path.join(__dirname, 'mai.psd');
const outDir = path.join(__dirname, 'internal', 'server', 'static', 'assets', 'mai2d');
if (!fs.existsSync(outDir)) fs.mkdirSync(outDir, { recursive: true });

PSD.open(psdPath).then(async psd => {
    const header = psd.header;
    console.log(`PSD: ${header.cols}x${header.rows}, ${header.channels} channels, depth ${header.depth}`);

    // Save merged image
    const mergedPath = path.join(outDir, '_merged.png');
    psd.image.saveAsPng(mergedPath);
    console.log(`Merged: ${mergedPath}`);

    // Extract individual layers via parsed tree
    const tree = psd.tree();
    const layers = [];

    function collectLayers(node, depth = 0) {
        if (node._children && node._children.length > 0) {
            node._children.forEach(child => collectLayers(child, depth + 1));
        } else if (node.layer) {
            layers.push({ node, depth });
        }
    }
    collectLayers(tree);

    console.log(`\nFound ${layers.length} leaf layers:`);
    for (const { node, depth } of layers) {
        const layer = node.layer;
        const name = node.name || 'unnamed';
        const visible = node.forceVisible || layer.visible;
        const w = layer.right - layer.left;
        const h = layer.bottom - layer.top;
        console.log(`  ${'  '.repeat(depth)}${visible ? '👁' : '🔲'} ${name} (${w}x${h})`);
    }

    // Try to export each layer
    console.log(`\nExporting layers...`);
    for (const { node, depth } of layers) {
        const layer = node.layer;
        const name = (node.name || 'layer').replace(/[^a-zA-Z0-9_-]/g, '_').toLowerCase();
        const safeName = name || `layer_${depth}`;
        const outPath = path.join(outDir, `${safeName}.png`);

        try {
            if (layer.image && typeof layer.image.toPng === 'function') {
                layer.image.saveAsPng(outPath);
                console.log(`  ✓ ${safeName}.png`);
            } else if (typeof node.toPng === 'function') {
                node.toPng().saveAsPng(outPath);
                console.log(`  ✓ ${safeName}.png (via node)`);
            } else {
                console.log(`  ✗ ${safeName} — no export method`);
            }
        } catch (e) {
            console.log(`  ✗ ${safeName} — ${e.message}`);
        }
    }
}).catch(err => {
    console.error('Error:', err.message);
});
