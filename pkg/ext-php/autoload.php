<?php

declare(strict_types=1);

// For use without Composer: `require 'path/to/ext-php/autoload.php';` registers the same PSR-4
// mapping as composer.json (ExtIO\ => src/) and loads the functions and constants.

spl_autoload_register(static function (string $class): void {
    if (str_starts_with($class, 'ExtIO\\')) {
        $file = __DIR__ . '/src/' . str_replace('\\', '/', substr($class, 6)) . '.php';
        if (is_file($file)) {
            require $file;
        }
    }
});

require_once __DIR__ . '/src/Plugin/functions.php';
