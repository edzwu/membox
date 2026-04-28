// Typewriter effect on homepage title
    const typewriterTitle = document.getElementById('typewriter-title');
    if (typewriterTitle) {
        const lines = Array.from(typewriterTitle.querySelectorAll('span'));
        const delayStepMs = 80;
        const queue = [];

        lines.forEach((line) => {
            const text = line.textContent;
            line.innerHTML = '';
            for (let char of text) {
                const span = document.createElement('span');
                span.className = 'type-char';
                span.textContent = char === ' ' ? '\u00A0' : char;
                queue.push({ span, line });
            }
        });

        // Cursor starts on the first line, then gets pushed forward as we type
        const cursor = document.createElement('span');
        cursor.className = 'type-cursor';
        cursor.textContent = '|';
        if (lines.length > 0) lines[0].appendChild(cursor);

        let i = 0;
        function typeNext() {
            if (i >= queue.length) return;
            const { span, line } = queue[i];
            if (cursor.parentNode !== line) line.appendChild(cursor);
            cursor.before(span);
            i++;
            setTimeout(typeNext, delayStepMs);
        }
        setTimeout(typeNext, delayStepMs);
    }

    // Article page: section nesting + ToC highlighting
    if (document.querySelector('main article')) {
        convertToNestedSections(document.querySelector('main article'));
        addParentHeadingAttribute();
        startNavObservation();
    }

    // Article page menu button
    var button = document.querySelector('#menu-button');
    var menu = document.querySelector('#TableOfContents');
    if (button && menu) {
        button.addEventListener('click', function (event) {
            document.body.classList.add("menu-open");
        });
        menu.addEventListener('click', function (event) {
            document.body.classList.remove("menu-open");
        });
    }

    // Homepage search
    const searchInput = document.getElementById('home-search');
    const noteList = document.getElementById('note-list');
    const noResults = document.getElementById('no-results');

    if (searchInput && noteList) {
        const rows = Array.from(noteList.querySelectorAll('.ls-row'));

        searchInput.addEventListener('input', () => {
            const query = searchInput.value.trim().toLowerCase();
            let visible = 0;

            rows.forEach(row => {
                const title = row.dataset.title || '';
                const tldr = row.dataset.tldr || '';
                const tags = row.dataset.tags || '';
                const match = title.includes(query) || tldr.includes(query) || tags.includes(query);
                row.hidden = !match;
                if (match) visible++;
            });

            if (noResults) {
                noResults.hidden = visible > 0 || query === '';
            }
        });
    }

    // Homepage hamburger menu

function convertToNestedSections(rootElement) {
    const children = Array.from(rootElement.children);

    children.forEach(element => rootElement.removeChild(element));

    let currentSection = rootElement;
    let currentLevel = 0;

    children.forEach(element => {
        const headingMatch = element.tagName.match(/^h(\d)$/i);

        if (headingMatch) {
            const newLevel = parseInt(headingMatch[1]);

            while (currentLevel + 1 < newLevel) {
                const section = document.createElement('section');
                currentSection.appendChild(section);
                currentSection = section;
                currentLevel++;
            }

            while (currentLevel + 1 > newLevel) {
                currentSection = currentSection.parentNode;
                currentLevel--;
            }

            const id = element.getAttribute('id');

            const newSection = document.createElement('section');
            newSection.setAttribute('id', id);
            element.removeAttribute('id');

            const permalink = document.createElement('a');
            permalink.setAttribute('href', `#${id}`);
            permalink.classList.add('permalink');
            element.appendChild(permalink);

            currentSection.appendChild(newSection);

            currentSection = newSection;
            currentLevel = newLevel;
        }

        currentSection.appendChild(element);
    });
}

function addParentHeadingAttribute() {
    const selector = 'h1,h2,h3,h4,h5,h6';
    const siteTitle = document.body.dataset.siteTitle || '';

    document.querySelectorAll(selector).forEach(heading => {
        heading.setAttribute('data-site-title', siteTitle);
        const parentHeading = heading.parentElement && heading.parentElement.parentElement
            ? heading.parentElement.parentElement.querySelector(selector)
            : null;

        if (parentHeading) {
            heading.setAttribute('data-parent-heading', parentHeading.textContent);
        }
    });
}

function highlightFirstActive() {
    document.querySelectorAll("nav li").forEach(link => {
        link.classList.remove('active')
    })

    let firstVisibleLink = document.querySelector('nav li.visible');
    if (firstVisibleLink) {
        let firstVisibleChild = firstVisibleLink.querySelector("li.visible");
        if (firstVisibleChild) {
            firstVisibleChild.classList.add('active')
        } else {
            firstVisibleLink.classList.add('active')
        }
    }
}

function startNavObservation() {
    const observer = new IntersectionObserver(entries => {
        entries.forEach(entry => {
            const id = entry.target.getAttribute('id');
            const link = document.querySelector(`nav li a[href="#${id}"]`);
            if (link) {
                if (entry.intersectionRatio > 0) {
                    link.parentElement.classList.add('visible');
                } else {
                    link.parentElement.classList.remove('visible');
                }
            }
        });
        highlightFirstActive();
    });

    document.querySelectorAll('section[id]').forEach((section) => {
        observer.observe(section);
    });
}
