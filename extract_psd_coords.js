const PSD = require('psd');
const fs = require('fs');
const path = require('path');

const psdPath = path.join(__dirname, 'mai.psd');

PSD.open(psdPath).then(psd => {
    const tree = psd.tree();
    const layers = [];

    function collectLayers(node) {
        if (node._children && node._children.length > 0) {
            node._children.forEach(child => collectLayers(child));
        } else if (node.layer) {
            layers.push({
                name: node.name || 'unnamed',
                left: node.layer.left,
                top: node.layer.top,
                right: node.layer.right,
                bottom: node.layer.bottom,
                visible: node.layer.visible,
                opacity: node.layer.opacity
            });
        }
    }
    collectLayers(tree);

    // Output as JSON
    const out = JSON.stringify({ width: psd.header.cols, height: psd.header.rows, layers }, null, 2);
    console.log(out);
}).catch(err => console.error(err.message));
