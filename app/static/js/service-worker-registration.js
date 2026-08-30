if ('serviceWorker' in navigator) {
    var mireServiceWorkerScope = new URL(mireUrl('/'), window.location.origin).href;
    var mireServiceWorkerPolicy = MireBrowserContracts.computeServiceWorkerPolicy(
        window.location.hostname, window.location.search, mireServiceWorkerScope
    );
    var mireCacheNamespace = mireServiceWorkerPolicy.cacheNamespace;
    if (mireServiceWorkerPolicy.action === 'cleanup') {
        navigator.serviceWorker.getRegistrations()
            .then(function(registrations) {
                return Promise.all(registrations.filter(function(registration) {
                    return registration.scope === mireServiceWorkerScope;
                }).map(function(registration) {
                    return registration.unregister();
                }));
            })
            .then(function() {
                if (!window.caches) return;
                return caches.keys().then(function(keys) {
                    return Promise.all(keys.filter(function(key) {
                        return key.indexOf(mireCacheNamespace) === 0;
                    }).map(function(key) { return caches.delete(key); }));
                });
            })
            .catch(function(err) { console.warn('SW cleanup failed:', err); });
    } else {
        navigator.serviceWorker.register(mireUrl('/sw.js'), { scope: mireUrl('/') })
            .catch(function(err) { console.warn('SW registration failed:', err); });
    }
}
