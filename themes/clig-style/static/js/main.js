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
        var scrollBeforeMenu = 0;
        function setMenuHeight() {
            var tocRoot = menu.closest('.page-toc') || menu;
            var rect = tocRoot.getBoundingClientRect();
            var height = Math.max(tocRoot.scrollHeight, rect.height, window.innerHeight);
            document.body.style.setProperty('--toc-scroll-height', Math.ceil(height) + 'px');
        }

        function closeMenu() {
            document.body.classList.remove("menu-open");
            document.body.style.removeProperty('--toc-scroll-height');
            window.scrollTo(0, scrollBeforeMenu);
        }

        button.addEventListener('click', function (event) {
            var willOpen = !document.body.classList.contains("menu-open");
            if (willOpen) {
                scrollBeforeMenu = window.scrollY || window.pageYOffset || 0;
                document.body.classList.add("menu-open");
                setMenuHeight();
                window.scrollTo(0, 0);
            } else {
                closeMenu();
            }
        });
        menu.addEventListener('click', function (event) {
            closeMenu();
        });
        window.addEventListener('resize', function() {
            if (document.body.classList.contains("menu-open")) {
                setMenuHeight();
            }
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

    // Language auto-redirect
    (function() {
        var savedLang = document.documentElement.dataset.savedLang;
        if (savedLang) {
            var currentLang = document.documentElement.lang;
            if (currentLang !== savedLang) {
                var toggle = document.querySelector('.lang-toggle-nav');
                if (toggle && toggle.dataset.targetUrl && toggle.dataset.targetLang === savedLang) {
                    window.location.href = toggle.dataset.targetUrl;
                }
            }
        }
    })();

    // Language toggle
    var langToggle = document.querySelector('.lang-toggle-nav');
    if (langToggle) {
        langToggle.addEventListener('click', function() {
            var targetUrl = langToggle.dataset.targetUrl;
            var targetLang = langToggle.dataset.targetLang;
            if (targetUrl) {
                localStorage.setItem('lang', targetLang);
                window.location.href = targetUrl;
            }
        });
    }

    // Theme toggle
    var themeToggle = document.querySelector('.theme-toggle');
    if (themeToggle) {
        var sunIcon = themeToggle.querySelector('.theme-icon-sun');
        var moonIcon = themeToggle.querySelector('.theme-icon-moon');
        var isHome = document.body.classList.contains('home');

        function isCurrentlyDark() {
            var t = document.documentElement.dataset.theme;
            if (t === 'dark') return true;
            if (t === 'light') return false;
            return isHome || window.matchMedia('(prefers-color-scheme: dark)').matches;
        }

        function updateThemeIcons() {
            if (isCurrentlyDark()) {
                if (sunIcon) sunIcon.style.display = 'none';
                if (moonIcon) moonIcon.style.display = 'block';
            } else {
                if (sunIcon) sunIcon.style.display = 'block';
                if (moonIcon) moonIcon.style.display = 'none';
            }
        }

        updateThemeIcons();

        themeToggle.addEventListener('click', function() {
            var t = document.documentElement.dataset.theme;
            if (t) {
                document.documentElement.removeAttribute('data-theme');
                localStorage.removeItem('theme');
            } else {
                var target = isCurrentlyDark() ? 'light' : 'dark';
                document.documentElement.dataset.theme = target;
                localStorage.setItem('theme', target);
            }
            updateThemeIcons();
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
