(function() {
    function initInteractiveUI() {
        ensureStableNavigation();
        bindNavRefreshLinks();
        initTypewriter();
        initArticlePageEnhancements();
        initArticleMenuButton();
        initHomepageSearch();
        runLanguageAutoRedirect();
        initLanguageToggle();
        initThemeToggle();
    }

    function ensureStableNavigation() {
        var body = document.body;
        if (!body) return;

        // Single article pages intentionally use ToC navigation, not top nav.
        var isArticlePage = body.classList.contains('single-page') && !body.classList.contains('about-page');
        if (isArticlePage) return;

        var nav = document.getElementById('site-nav') || document.querySelector('.site-nav');
        if (!nav) {
            nav = rebuildSiteNavFromBodyData();
        }

        if (!nav) return;

        nav.hidden = false;
        nav.style.display = 'flex';
        refreshTopNavCurrentPage(nav);
    }

    function rebuildSiteNavFromBodyData() {
        var body = document.body;
        if (!body) return null;

        var homeUrl = body.dataset.homeUrl || '/';
        var notesUrl = body.dataset.notesUrl || '/notes/';
        var aboutUrl = body.dataset.aboutUrl || '/about/';
        var homeLabel = body.dataset.navHomeLabel || 'Home';
        var notesLabel = body.dataset.navNotesLabel || 'Notes';
        var aboutLabel = body.dataset.navAboutLabel || 'About';

        var header = document.createElement('header');
        header.className = 'site-nav';
        header.id = 'site-nav';

        var logoLink = document.createElement('a');
        logoLink.href = homeUrl;
        logoLink.className = 'nav-logo';
        logoLink.setAttribute('data-nav-refresh', 'true');
        logoLink.setAttribute('aria-label', 'Home');
        logoLink.innerHTML = '<svg viewBox="0 0 40 40" width="28" height="28" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><rect x="4" y="4" width="32" height="32" rx="5"></rect><polyline points="12,14 8,20 12,26"></polyline><line x1="17" y1="26" x2="28" y2="26"></line></svg>';

        var navLinks = document.createElement('nav');
        navLinks.className = 'nav-links';

        navLinks.appendChild(createTopNavLink(homeUrl, homeLabel));
        navLinks.appendChild(createTopNavLink(notesUrl, notesLabel));
        navLinks.appendChild(createTopNavLink(aboutUrl, aboutLabel));

        header.appendChild(logoLink);
        header.appendChild(navLinks);

        var main = document.querySelector('main');
        if (main && main.parentNode) {
            main.parentNode.insertBefore(header, main);
        } else {
            body.insertBefore(header, body.firstChild);
        }

        return header;
    }

    function createTopNavLink(url, text) {
        var link = document.createElement('a');
        link.href = url;
        link.textContent = text;
        link.setAttribute('data-nav-refresh', 'true');
        return link;
    }

    function refreshTopNavCurrentPage(nav) {
        var links = nav.querySelectorAll('.nav-links a');
        var path = window.location.pathname || '/';

        links.forEach(function(link) {
            var href = link.getAttribute('href') || '';
            link.removeAttribute('aria-current');

            var isHome = href === '/' || href.endsWith('/en/') || href.endsWith('/zh/');
            var isNotes = href.indexOf('/notes/') !== -1;
            var isAbout = href.indexOf('/about/') !== -1;

            if ((isHome && isHomePath(path)) || (isNotes && path.indexOf('/notes/') !== -1) || (isAbout && path.indexOf('/about/') !== -1)) {
                link.setAttribute('aria-current', 'page');
            }
        });
    }

    function isHomePath(path) {
        var normalized = normalizePath(path);
        return normalized === '/' || normalized === '/en/' || normalized === '/zh/';
    }

    function normalizePath(path) {
        if (!path) return '/';
        if (path.charAt(path.length - 1) !== '/') return path + '/';
        return path;
    }

    function bindNavRefreshLinks() {
        var links = document.querySelectorAll('a[data-nav-refresh="true"]');
        links.forEach(function(link) {
            if (link.dataset.navRefreshBound === '1') return;
            link.dataset.navRefreshBound = '1';

            link.addEventListener('click', function() {
                try {
                    sessionStorage.setItem('agora-nav-refresh-needed', '1');
                } catch (err) {
                    // Ignore storage failures in privacy-restricted browsers.
                }
            });
        });
    }

    function consumePendingNavRefresh() {
        try {
            if (sessionStorage.getItem('agora-nav-refresh-needed') === '1') {
                sessionStorage.removeItem('agora-nav-refresh-needed');
                ensureStableNavigation();
            }
        } catch (err) {
            // Ignore storage failures in privacy-restricted browsers.
        }
    }

    function initTypewriter() {
        var typewriterTitle = document.getElementById('typewriter-title');
        if (!typewriterTitle || typewriterTitle.dataset.typed === '1') return;

        typewriterTitle.dataset.typed = '1';

        var lines = Array.from(typewriterTitle.querySelectorAll('span'));
        var delayStepMs = 80;
        var queue = [];

        lines.forEach(function(line) {
            var text = line.textContent || '';
            line.innerHTML = '';
            for (var i = 0; i < text.length; i++) {
                var span = document.createElement('span');
                span.className = 'type-char';
                span.textContent = text[i] === ' ' ? '\u00A0' : text[i];
                queue.push({ span: span, line: line });
            }
        });

        var cursor = document.createElement('span');
        cursor.className = 'type-cursor';
        cursor.textContent = '|';
        if (lines.length > 0) lines[0].appendChild(cursor);

        var index = 0;
        function typeNext() {
            if (index >= queue.length) return;
            var item = queue[index];
            if (cursor.parentNode !== item.line) item.line.appendChild(cursor);
            cursor.before(item.span);
            index += 1;
            setTimeout(typeNext, delayStepMs);
        }

        setTimeout(typeNext, delayStepMs);
    }

    function initArticlePageEnhancements() {
        var article = document.querySelector('main article');
        if (!article) return;

        if (article.dataset.nestedSectionsDone !== '1') {
            convertToNestedSections(article);
            article.dataset.nestedSectionsDone = '1';
        }

        if (document.body.dataset.parentHeadingDone !== '1') {
            addParentHeadingAttribute();
            document.body.dataset.parentHeadingDone = '1';
        }

        if (article.dataset.navObserveDone !== '1') {
            startNavObservation();
            article.dataset.navObserveDone = '1';
        }
    }

    function initArticleMenuButton() {
        var button = document.querySelector('#menu-button');
        var menu = document.querySelector('#TableOfContents');
        if (!button || !menu || button.dataset.menuBound === '1') return;

        button.dataset.menuBound = '1';

        var scrollBeforeMenu = 0;

        function setMenuHeight() {
            var tocRoot = menu.closest('.page-toc') || menu;
            var rect = tocRoot.getBoundingClientRect();
            var height = Math.max(tocRoot.scrollHeight, rect.height, window.innerHeight);
            document.body.style.setProperty('--toc-scroll-height', Math.ceil(height) + 'px');
        }

        function closeMenu() {
            document.body.classList.remove('menu-open');
            document.body.style.removeProperty('--toc-scroll-height');
            window.scrollTo(0, scrollBeforeMenu);
        }

        button.addEventListener('click', function() {
            var willOpen = !document.body.classList.contains('menu-open');
            if (willOpen) {
                scrollBeforeMenu = window.scrollY || window.pageYOffset || 0;
                document.body.classList.add('menu-open');
                setMenuHeight();
                window.scrollTo(0, 0);
            } else {
                closeMenu();
            }
        });

        menu.addEventListener('click', function() {
            closeMenu();
        });

        window.addEventListener('resize', function() {
            if (document.body.classList.contains('menu-open')) {
                setMenuHeight();
            }
        });
    }

    function initHomepageSearch() {
        var searchInput = document.getElementById('home-search');
        var noteList = document.getElementById('note-list');
        var noResults = document.getElementById('no-results');

        if (!searchInput || !noteList || searchInput.dataset.searchBound === '1') return;
        searchInput.dataset.searchBound = '1';

        var rows = Array.from(noteList.querySelectorAll('.ls-row'));

        searchInput.addEventListener('input', function() {
            var query = (searchInput.value || '').trim().toLowerCase();
            var visible = 0;

            rows.forEach(function(row) {
                var title = row.dataset.title || '';
                var tldr = row.dataset.tldr || '';
                var tags = row.dataset.tags || '';
                var match = title.indexOf(query) !== -1 || tldr.indexOf(query) !== -1 || tags.indexOf(query) !== -1;
                row.hidden = !match;
                if (match) visible += 1;
            });

            if (noResults) {
                noResults.hidden = visible > 0 || query === '';
            }
        });
    }

    function runLanguageAutoRedirect() {
        if (document.documentElement.dataset.langRedirectChecked === '1') return;
        document.documentElement.dataset.langRedirectChecked = '1';

        var savedLang = document.documentElement.dataset.savedLang;
        if (!savedLang) return;

        var currentLang = document.documentElement.lang;
        if (currentLang === savedLang) return;

        var toggle = document.querySelector('.lang-toggle-nav');
        if (toggle && toggle.dataset.targetUrl && toggle.dataset.targetLang === savedLang) {
            window.location.href = toggle.dataset.targetUrl;
        }
    }

    function initLanguageToggle() {
        var langToggle = document.querySelector('.lang-toggle-nav');
        if (!langToggle || langToggle.dataset.langBound === '1') return;
        langToggle.dataset.langBound = '1';

        langToggle.addEventListener('click', function() {
            var targetUrl = langToggle.dataset.targetUrl;
            var targetLang = langToggle.dataset.targetLang;
            if (!targetUrl) return;

            localStorage.setItem('lang', targetLang);
            window.location.href = targetUrl;
        });
    }

    function initThemeToggle() {
        var themeToggle = document.querySelector('.theme-toggle');
        if (!themeToggle) return;

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

        if (themeToggle.dataset.themeBound === '1') return;
        themeToggle.dataset.themeBound = '1';

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

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', initInteractiveUI, { once: true });
    } else {
        initInteractiveUI();
    }

    window.addEventListener('pageshow', function() {
        initInteractiveUI();
        consumePendingNavRefresh();
    });

    window.addEventListener('popstate', function() {
        setTimeout(function() {
            initInteractiveUI();
            consumePendingNavRefresh();
        }, 0);
    });
})();

function convertToNestedSections(rootElement) {
    const children = Array.from(rootElement.children);

    children.forEach(element => rootElement.removeChild(element));

    let currentSection = rootElement;
    let currentLevel = 0;

    children.forEach(element => {
        const headingMatch = element.tagName.match(/^h(\d)$/i);

        if (headingMatch) {
            const newLevel = parseInt(headingMatch[1], 10);

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
            if (id) {
                newSection.setAttribute('id', id);
                element.removeAttribute('id');

                const permalink = document.createElement('a');
                permalink.setAttribute('href', `#${id}`);
                permalink.classList.add('permalink');
                element.appendChild(permalink);
            }

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
    document.querySelectorAll('nav li').forEach(link => {
        link.classList.remove('active');
    });

    const firstVisibleLink = document.querySelector('nav li.visible');
    if (firstVisibleLink) {
        const firstVisibleChild = firstVisibleLink.querySelector('li.visible');
        if (firstVisibleChild) {
            firstVisibleChild.classList.add('active');
        } else {
            firstVisibleLink.classList.add('active');
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

    document.querySelectorAll('section[id]').forEach(section => {
        observer.observe(section);
    });
}
