<?php

declare(strict_types=1);

namespace ExtIO\Plugin;

/** @internal Loading and validating ext.plugin/v1 descriptors. */
final class Descriptor
{
    private const MAX_FILE_BYTES = 16 << 20;

    public static function validate(mixed $d): void
    {
        $d = Json::normalize($d);
        if (!Json::strict($d, ['apiVersion', 'identity', 'contracts', 'reverse'])) {
            self::invalid('descriptor must be an object with apiVersion, identity, contracts');
        }
        if (Json::get($d, 'apiVersion') !== API_VERSION) {
            self::invalid('unsupported apiVersion');
        }
        $ident = Json::get($d, 'identity');
        if (!Json::strict($ident, ['id', 'revision', 'version'])) {
            self::invalid('invalid identity');
        }
        if (!Pattern::identifier(Json::get($ident, 'id')) || !Pattern::revision(Json::get($ident, 'revision'))) {
            self::invalid('identity requires a bounded id and immutable revision');
        }
        if (Json::has($ident, 'version') && $ident->version !== '' && !Pattern::revision($ident->version)) {
            self::invalid('invalid identity version');
        }
        $contracts = Json::get($d, 'contracts');
        if (!\is_array($contracts) || \count($contracts) < 1 || \count($contracts) > 64) {
            self::invalid('declare 1..64 contracts');
        }
        self::contracts($contracts, false);
        // The host operations the plugin may call; none is a null or empty list.
        $reverse = Json::get($d, 'reverse');
        if ($reverse !== null) {
            if (!\is_array($reverse) || \count($reverse) > 64) {
                self::invalid('declare at most 64 reverse contracts');
            }
            self::contracts($reverse, true);
        }
    }

    public static function load(string $path): \stdClass
    {
        $raw = @file_get_contents($path, false, null, 0, self::MAX_FILE_BYTES + 1);
        if ($raw === false) {
            self::invalid('cannot read descriptor file ' . $path);
        }
        if (\strlen($raw) > self::MAX_FILE_BYTES) {
            self::invalid('descriptor file exceeds 16 MiB');
        }
        try {
            $data = Json::decode($raw);
        } catch (ProtocolError $e) {
            throw new \InvalidArgumentException($e->getMessage(), 0, $e);
        }
        if ($data instanceof \stdClass && Json::has($data, 'descriptor')) {
            if (Json::get($data, 'apiVersion') !== 'ext.package/v1') {
                self::invalid('unsupported package apiVersion');
            }
            $data = $data->descriptor;
        }
        self::validate($data);
        return $data;
    }

    private static function contracts(array $contracts, bool $reverse): void
    {
        $seen = [];
        foreach ($contracts as $c) {
            if (!Json::strict($c, ['name', 'version', 'operations'])) {
                self::invalid('invalid contract');
            }
            if (!Pattern::identifier(Json::get($c, 'name')) || !Pattern::revision(Json::get($c, 'version'))) {
                self::invalid('contract requires a name and exact version');
            }
            $key = $c->name . "\0" . $c->version;
            $ops = Json::get($c, 'operations');
            if (isset($seen[$key]) || !\is_array($ops) || \count($ops) < 1 || \count($ops) > 256) {
                self::invalid('duplicate contract or invalid operation count');
            }
            $seen[$key] = true;
            $names = [];
            foreach ($ops as $op) {
                if (!Json::strict($op, ['name', 'surface', 'progress'])) {
                    self::invalid('invalid operation');
                }
                $name = Json::get($op, 'name');
                // Go leaves a member that is null alone, as if absent.
                $surface = Json::get($op, 'surface') ?? '';
                $progress = Json::get($op, 'progress');
                if (!Pattern::identifier($name)
                    || $name === 'plugin.hello'
                    || isset($names[$name])
                    || !\is_string($surface)
                    || ($surface !== '' && !Pattern::identifier($surface))
                    || ($progress !== null && !\is_bool($progress))
                    || ($progress === true && $reverse) // a host operation does not report progress
                ) {
                    self::invalid('invalid, reserved, or duplicate operation');
                }
                $names[$name] = true;
            }
        }
    }

    private static function invalid(string $why): never
    {
        throw new \InvalidArgumentException($why);
    }
}
