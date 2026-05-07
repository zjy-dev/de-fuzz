const pptxgen = require('pptxgenjs');
const html2pptx = require('/root/project/de-fuzz/.factory/skills/pptx/scripts/html2pptx.js');
const path = require('path');

async function createPresentation() {
    const pptx = new pptxgen();
    pptx.layout = 'LAYOUT_16x9';
    pptx.author = 'DeFuzz';
    pptx.title = 'DeFuzz: LLM驱动的编译器软件防御机制模糊测试 - 开题答辩';
    pptx.subject = '硕士学位论文开题答辩';

    const slidesDir = path.join(__dirname, 'slides');

    // Define slide master for page numbers
    pptx.defineSlideMaster({
        title: 'MASTER_SLIDE',
        background: { color: 'FFFFFF' },
        objects: [
            { placeholder: { options: { name: 'slideNumber', type: 'slideNumber', x: 9.3, y: 5.2, w: 0.5, h: 0.3, fontSize: 10, color: '666666' } } }
        ],
        slideNumber: { x: 9.3, y: 5.2, fontSize: 10, color: '666666' }
    });

    const slides = [
        'slide01-title.html',
        'slide02-background.html',
        'slide03-status.html',
        'slide04-goals.html',
        'slide05-content.html',
        'slide06-method.html',
        'slide06b-tech.html',
        'slide07-canary-oracle.html',
        'slide07-experiment.html',
        'slide08-expected.html',
        'slide09-schedule.html',
        'slide10-thanks.html'
    ];

    for (let i = 0; i < slides.length; i++) {
        const slidePath = path.join(slidesDir, slides[i]);
        console.log(`Processing ${slides[i]}...`);
        
        const { slide } = await html2pptx(slidePath, pptx);
        
        // Add page number (skip title and thanks slides)
        if (i > 0 && i < slides.length - 1) {
            slide.addText(`${i}`, {
                x: 9.3,
                y: 5.2,
                w: 0.5,
                h: 0.3,
                fontSize: 10,
                color: '666666',
                align: 'right'
            });
        }
    }

    const outputPath = path.join(__dirname, '开题答辩-DeFuzz.pptx');
    await pptx.writeFile({ fileName: outputPath });
    console.log(`Presentation created: ${outputPath}`);
}

createPresentation().catch(err => {
    console.error('Error:', err.message);
    process.exit(1);
});
