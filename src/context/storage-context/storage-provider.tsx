import React, { useCallback, useMemo, useRef } from 'react';
import type { StorageContext } from './storage-context';
import { storageContext } from './storage-context';
import type { Diagram } from '@/lib/domain/diagram';
import type { DBTable } from '@/lib/domain/db-table';
import type { DBRelationship } from '@/lib/domain/db-relationship';
import type { ChartDBConfig } from '@/lib/domain/config';
import type { DBDependency } from '@/lib/domain/db-dependency';
import type { Area } from '@/lib/domain/area';
import type { DBCustomType } from '@/lib/domain/db-custom-type';
import type { DiagramFilter } from '@/lib/domain/diagram-filter/diagram-filter';
import type { Note } from '@/lib/domain/note';
import { API_BASE_URL } from '@/lib/env';

type DiagramListOptions = {
    includeTables?: boolean;
    includeRelationships?: boolean;
    includeDependencies?: boolean;
    includeAreas?: boolean;
    includeCustomTypes?: boolean;
    includeNotes?: boolean;
};

type DiagramCollectionKey =
    | 'tables'
    | 'relationships'
    | 'dependencies'
    | 'areas'
    | 'customTypes'
    | 'notes';

const defaultConfig: ChartDBConfig = {
    defaultDiagramId: '',
};

const hasIncludeOptions = (options?: DiagramListOptions): boolean =>
    !!(
        options?.includeTables ||
        options?.includeRelationships ||
        options?.includeDependencies ||
        options?.includeAreas ||
        options?.includeCustomTypes ||
        options?.includeNotes
    );

const toDate = (value: unknown): Date => {
    if (value instanceof Date) {
        return value;
    }

    if (typeof value === 'string' || typeof value === 'number') {
        const parsed = new Date(value);
        if (!Number.isNaN(parsed.getTime())) {
            return parsed;
        }
    }

    return new Date();
};

const toDiagram = (value: unknown): Diagram => {
    const raw = (value ?? {}) as Record<string, unknown>;

    return {
        id: String(raw.id ?? ''),
        name: String(raw.name ?? ''),
        databaseType: String(raw.databaseType ?? 'generic') as Diagram['databaseType'],
        databaseEdition:
            typeof raw.databaseEdition === 'string'
                ? (raw.databaseEdition as Diagram['databaseEdition'])
                : undefined,
        tables: Array.isArray(raw.tables)
            ? (raw.tables as DBTable[])
            : undefined,
        relationships: Array.isArray(raw.relationships)
            ? (raw.relationships as DBRelationship[])
            : undefined,
        dependencies: Array.isArray(raw.dependencies)
            ? (raw.dependencies as DBDependency[])
            : undefined,
        areas: Array.isArray(raw.areas) ? (raw.areas as Area[]) : undefined,
        customTypes: Array.isArray(raw.customTypes)
            ? (raw.customTypes as DBCustomType[])
            : undefined,
        notes: Array.isArray(raw.notes) ? (raw.notes as Note[]) : undefined,
        createdAt: toDate(raw.createdAt),
        updatedAt: toDate(raw.updatedAt),
    };
};

const serializeDiagram = (diagram: Diagram): Record<string, unknown> => ({
    ...diagram,
    createdAt: diagram.createdAt.toISOString(),
    updatedAt: diagram.updatedAt.toISOString(),
});

const cloneDiagram = (diagram: Diagram): Diagram =>
    toDiagram(
        JSON.parse(JSON.stringify(serializeDiagram(diagram))) as Record<
            string,
            unknown
        >
    );

const serializeAttributes = (
    attributes: Partial<Diagram>
): Record<string, unknown> => {
    const serialized: Record<string, unknown> = {};

    for (const [key, value] of Object.entries(attributes)) {
        if (value instanceof Date) {
            serialized[key] = value.toISOString();
            continue;
        }

        serialized[key] = value;
    }

    return serialized;
};

export const StorageProvider: React.FC<React.PropsWithChildren> = ({
    children,
}) => {
    const diagramsCacheRef = useRef<Map<string, Diagram>>(new Map());
    const tableOwnersRef = useRef<Map<string, string>>(new Map());
    const relationshipOwnersRef = useRef<Map<string, string>>(new Map());
    const dependencyOwnersRef = useRef<Map<string, string>>(new Map());
    const areaOwnersRef = useRef<Map<string, string>>(new Map());
    const customTypeOwnersRef = useRef<Map<string, string>>(new Map());
    const noteOwnersRef = useRef<Map<string, string>>(new Map());
    const mutationQueueRef = useRef<Map<string, Promise<void>>>(new Map());

    const request = useCallback(
        async <T,>(path: string, init?: RequestInit): Promise<T> => {
            const response = await fetch(`${API_BASE_URL}${path}`, {
                ...init,
                headers: {
                    'Content-Type': 'application/json',
                    ...(init?.headers ?? {}),
                },
            });

            if (!response.ok) {
                let errorMessage = `Request failed with status ${response.status}`;

                try {
                    const payload = (await response.json()) as {
                        error?: string;
                    };
                    if (payload.error) {
                        errorMessage = payload.error;
                    }
                } catch {
                    // Ignore parse failures and keep default message.
                }

                throw new Error(errorMessage);
            }

            if (response.status === 204) {
                return undefined as T;
            }

            return (await response.json()) as T;
        },
        []
    );

    const clearOwnersForDiagram = useCallback((diagram: Diagram) => {
        diagram.tables?.forEach((table) => {
            tableOwnersRef.current.delete(table.id);
        });
        diagram.relationships?.forEach((relationship) => {
            relationshipOwnersRef.current.delete(relationship.id);
        });
        diagram.dependencies?.forEach((dependency) => {
            dependencyOwnersRef.current.delete(dependency.id);
        });
        diagram.areas?.forEach((area) => {
            areaOwnersRef.current.delete(area.id);
        });
        diagram.customTypes?.forEach((customType) => {
            customTypeOwnersRef.current.delete(customType.id);
        });
        diagram.notes?.forEach((note) => {
            noteOwnersRef.current.delete(note.id);
        });
    }, []);

    const indexDiagramOwners = useCallback((diagram: Diagram) => {
        diagram.tables?.forEach((table) => {
            tableOwnersRef.current.set(table.id, diagram.id);
        });
        diagram.relationships?.forEach((relationship) => {
            relationshipOwnersRef.current.set(relationship.id, diagram.id);
        });
        diagram.dependencies?.forEach((dependency) => {
            dependencyOwnersRef.current.set(dependency.id, diagram.id);
        });
        diagram.areas?.forEach((area) => {
            areaOwnersRef.current.set(area.id, diagram.id);
        });
        diagram.customTypes?.forEach((customType) => {
            customTypeOwnersRef.current.set(customType.id, diagram.id);
        });
        diagram.notes?.forEach((note) => {
            noteOwnersRef.current.set(note.id, diagram.id);
        });
    }, []);

    const setCachedDiagram = useCallback(
        (diagram: Diagram) => {
            const previous = diagramsCacheRef.current.get(diagram.id);
            if (previous) {
                clearOwnersForDiagram(previous);
            }

            diagramsCacheRef.current.set(diagram.id, diagram);
            indexDiagramOwners(diagram);
        },
        [clearOwnersForDiagram, indexDiagramOwners]
    );

    const removeCachedDiagram = useCallback(
        (diagramId: string) => {
            const existing = diagramsCacheRef.current.get(diagramId);
            if (existing) {
                clearOwnersForDiagram(existing);
            }

            diagramsCacheRef.current.delete(diagramId);
            mutationQueueRef.current.delete(diagramId);
        },
        [clearOwnersForDiagram]
    );

    const enqueueMutation = useCallback(
        <T,>(diagramId: string, operation: () => Promise<T>): Promise<T> => {
            const previous = mutationQueueRef.current.get(diagramId) ??
                Promise.resolve();

            const next = previous
                .catch(() => undefined)
                .then(operation);

            mutationQueueRef.current.set(
                diagramId,
                next.then(
                    () => undefined,
                    () => undefined
                )
            );

            return next;
        },
        []
    );

    const fetchDiagram = useCallback(
        async (diagramId: string): Promise<Diagram> => {
            const cached = diagramsCacheRef.current.get(diagramId);
            if (cached) {
                return cloneDiagram(cached);
            }

            const raw = await request<unknown>(
                `/diagrams/${encodeURIComponent(diagramId)}`
            );
            const diagram = toDiagram(raw);
            setCachedDiagram(diagram);
            return cloneDiagram(diagram);
        },
        [request, setCachedDiagram]
    );

    const saveDiagram = useCallback(
        async (diagram: Diagram): Promise<Diagram> => {
            const raw = await request<unknown>(
                `/diagrams/${encodeURIComponent(diagram.id)}`,
                {
                    method: 'PUT',
                    body: JSON.stringify(serializeDiagram(diagram)),
                }
            );

            const saved = toDiagram(raw);
            setCachedDiagram(saved);
            return cloneDiagram(saved);
        },
        [request, setCachedDiagram]
    );

    const mutateDiagram = useCallback(
        async (
            diagramId: string,
            mutator: (diagram: Diagram) => void
        ): Promise<Diagram> => {
            return enqueueMutation(diagramId, async () => {
                const diagram = await fetchDiagram(diagramId);
                mutator(diagram);
                return await saveDiagram(diagram);
            });
        },
        [enqueueMutation, fetchDiagram, saveDiagram]
    );

    const resolveDiagramIdByEntity = useCallback(
        async (
            entityId: string,
            ownersMapRef: React.MutableRefObject<Map<string, string>>,
            collectionKey: DiagramCollectionKey
        ): Promise<string> => {
            const cachedOwner = ownersMapRef.current.get(entityId);
            if (cachedOwner) {
                return cachedOwner;
            }

            for (const diagram of diagramsCacheRef.current.values()) {
                const items = diagram[collectionKey] as
                    | Array<{ id: string }>
                    | undefined;
                if (items?.some((item) => item.id === entityId)) {
                    ownersMapRef.current.set(entityId, diagram.id);
                    return diagram.id;
                }
            }

            const rawList = await request<unknown[]>('/diagrams?full=1');
            for (const raw of rawList) {
                const diagram = toDiagram(raw);
                setCachedDiagram(diagram);

                const items = diagram[collectionKey] as
                    | Array<{ id: string }>
                    | undefined;
                if (items?.some((item) => item.id === entityId)) {
                    ownersMapRef.current.set(entityId, diagram.id);
                    return diagram.id;
                }
            }

            throw new Error(`Entity ${entityId} was not found`);
        },
        [request, setCachedDiagram]
    );

    const getConfig: StorageContext['getConfig'] = useCallback(async () => {
        const config = await request<ChartDBConfig>('/config');
        return {
            ...defaultConfig,
            ...config,
        };
    }, [request]);

    const updateConfig: StorageContext['updateConfig'] = useCallback(
        async (config) => {
            await request<ChartDBConfig>('/config', {
                method: 'PUT',
                body: JSON.stringify(config),
            });
        },
        [request]
    );

    const getDiagramFilter: StorageContext['getDiagramFilter'] = useCallback(
        async (diagramId) => {
            try {
                return await request<DiagramFilter>(
                    `/diagrams/${encodeURIComponent(diagramId)}/filter`
                );
            } catch {
                return undefined;
            }
        },
        [request]
    );

    const updateDiagramFilter: StorageContext['updateDiagramFilter'] =
        useCallback(
            async (diagramId, filter) => {
                await request<DiagramFilter>(
                    `/diagrams/${encodeURIComponent(diagramId)}/filter`,
                    {
                        method: 'PUT',
                        body: JSON.stringify(filter),
                    }
                );
            },
            [request]
        );

    const deleteDiagramFilter: StorageContext['deleteDiagramFilter'] =
        useCallback(
            async (diagramId) => {
                await request<void>(
                    `/diagrams/${encodeURIComponent(diagramId)}/filter`,
                    {
                        method: 'DELETE',
                    }
                );
            },
            [request]
        );

    const addDiagram: StorageContext['addDiagram'] = useCallback(
        async ({ diagram }) => {
            const raw = await request<unknown>('/diagrams', {
                method: 'POST',
                body: JSON.stringify(serializeDiagram(diagram)),
            });
            setCachedDiagram(toDiagram(raw));
        },
        [request, setCachedDiagram]
    );

    const listDiagrams: StorageContext['listDiagrams'] = useCallback(
        async (options) => {
            if (hasIncludeOptions(options)) {
                const raw = await request<unknown[]>('/diagrams?full=1');
                const diagrams = raw.map(toDiagram);
                diagrams.forEach((diagram) => {
                    setCachedDiagram(diagram);
                });

                return diagrams;
            }

            const raw = await request<unknown[]>('/diagrams');
            const diagrams = raw.map(toDiagram);
            diagrams.forEach((diagram) => {
                const existing = diagramsCacheRef.current.get(diagram.id);
                if (existing) {
                    setCachedDiagram({
                        ...existing,
                        id: diagram.id,
                        name: diagram.name,
                        databaseType: diagram.databaseType,
                        databaseEdition: diagram.databaseEdition,
                        createdAt: diagram.createdAt,
                        updatedAt: diagram.updatedAt,
                    });
                    return;
                }

                setCachedDiagram(diagram);
            });

            return diagrams;
        },
        [request, setCachedDiagram]
    );

    const getDiagram: StorageContext['getDiagram'] = useCallback(
        async (id, options) => {
            const diagram = await fetchDiagram(id);

            if (hasIncludeOptions(options)) {
                return diagram;
            }

            return {
                id: diagram.id,
                name: diagram.name,
                databaseType: diagram.databaseType,
                databaseEdition: diagram.databaseEdition,
                createdAt: diagram.createdAt,
                updatedAt: diagram.updatedAt,
            };
        },
        [fetchDiagram]
    );

    const updateDiagram: StorageContext['updateDiagram'] = useCallback(
        async ({ id, attributes }) => {
            await enqueueMutation(id, async () => {
                const raw = await request<unknown>(
                    `/diagrams/${encodeURIComponent(id)}`,
                    {
                        method: 'PATCH',
                        body: JSON.stringify(serializeAttributes(attributes)),
                    }
                );
                const updated = toDiagram(raw);

                if (updated.id !== id) {
                    removeCachedDiagram(id);
                }

                setCachedDiagram(updated);
            });
        },
        [enqueueMutation, request, removeCachedDiagram, setCachedDiagram]
    );

    const deleteDiagram: StorageContext['deleteDiagram'] = useCallback(
        async (id) => {
            await enqueueMutation(id, async () => {
                await request<void>(`/diagrams/${encodeURIComponent(id)}`, {
                    method: 'DELETE',
                });
                removeCachedDiagram(id);
            });
        },
        [enqueueMutation, request, removeCachedDiagram]
    );

    const addTable: StorageContext['addTable'] = useCallback(
        async ({ diagramId, table }) => {
            await mutateDiagram(diagramId, (diagram) => {
                const tables = diagram.tables ?? [];
                diagram.tables = [...tables, table];
            });
        },
        [mutateDiagram]
    );

    const getTable: StorageContext['getTable'] = useCallback(
        async ({ diagramId, id }) => {
            const diagram = await fetchDiagram(diagramId);
            return diagram.tables?.find((table) => table.id === id);
        },
        [fetchDiagram]
    );

    const updateTable: StorageContext['updateTable'] = useCallback(
        async ({ id, attributes }) => {
            const diagramId = await resolveDiagramIdByEntity(
                id,
                tableOwnersRef,
                'tables'
            );
            await mutateDiagram(diagramId, (diagram) => {
                const tables = diagram.tables ?? [];
                diagram.tables = tables.map((table) =>
                    table.id === id ? { ...table, ...attributes } : table
                );
            });
        },
        [mutateDiagram, resolveDiagramIdByEntity]
    );

    const putTable: StorageContext['putTable'] = useCallback(
        async ({ diagramId, table }) => {
            await mutateDiagram(diagramId, (diagram) => {
                const tables = diagram.tables ?? [];
                const hasTable = tables.some((item) => item.id === table.id);
                diagram.tables = hasTable
                    ? tables.map((item) => (item.id === table.id ? table : item))
                    : [...tables, table];
            });
        },
        [mutateDiagram]
    );

    const deleteTable: StorageContext['deleteTable'] = useCallback(
        async ({ diagramId, id }) => {
            await mutateDiagram(diagramId, (diagram) => {
                diagram.tables = (diagram.tables ?? []).filter(
                    (table) => table.id !== id
                );
            });
        },
        [mutateDiagram]
    );

    const listTables: StorageContext['listTables'] = useCallback(
        async (diagramId) => {
            const diagram = await fetchDiagram(diagramId);
            return diagram.tables ?? [];
        },
        [fetchDiagram]
    );

    const deleteDiagramTables: StorageContext['deleteDiagramTables'] =
        useCallback(
            async (diagramId) => {
                await mutateDiagram(diagramId, (diagram) => {
                    diagram.tables = [];
                });
            },
            [mutateDiagram]
        );

    const addRelationship: StorageContext['addRelationship'] = useCallback(
        async ({ diagramId, relationship }) => {
            await mutateDiagram(diagramId, (diagram) => {
                const relationships = diagram.relationships ?? [];
                diagram.relationships = [...relationships, relationship];
            });
        },
        [mutateDiagram]
    );

    const getRelationship: StorageContext['getRelationship'] = useCallback(
        async ({ diagramId, id }) => {
            const diagram = await fetchDiagram(diagramId);
            return diagram.relationships?.find(
                (relationship) => relationship.id === id
            );
        },
        [fetchDiagram]
    );

    const updateRelationship: StorageContext['updateRelationship'] =
        useCallback(
            async ({ id, attributes }) => {
                const diagramId = await resolveDiagramIdByEntity(
                    id,
                    relationshipOwnersRef,
                    'relationships'
                );
                await mutateDiagram(diagramId, (diagram) => {
                    diagram.relationships = (diagram.relationships ?? []).map(
                        (relationship) =>
                            relationship.id === id
                                ? { ...relationship, ...attributes }
                                : relationship
                    );
                });
            },
            [mutateDiagram, resolveDiagramIdByEntity]
        );

    const deleteRelationship: StorageContext['deleteRelationship'] =
        useCallback(
            async ({ diagramId, id }) => {
                await mutateDiagram(diagramId, (diagram) => {
                    diagram.relationships = (diagram.relationships ?? []).filter(
                        (relationship) => relationship.id !== id
                    );
                });
            },
            [mutateDiagram]
        );

    const listRelationships: StorageContext['listRelationships'] = useCallback(
        async (diagramId) => {
            const diagram = await fetchDiagram(diagramId);
            return [...(diagram.relationships ?? [])].sort((a, b) =>
                a.name.localeCompare(b.name)
            );
        },
        [fetchDiagram]
    );

    const deleteDiagramRelationships: StorageContext['deleteDiagramRelationships'] =
        useCallback(
            async (diagramId) => {
                await mutateDiagram(diagramId, (diagram) => {
                    diagram.relationships = [];
                });
            },
            [mutateDiagram]
        );

    const addDependency: StorageContext['addDependency'] = useCallback(
        async ({ diagramId, dependency }) => {
            await mutateDiagram(diagramId, (diagram) => {
                const dependencies = diagram.dependencies ?? [];
                diagram.dependencies = [...dependencies, dependency];
            });
        },
        [mutateDiagram]
    );

    const getDependency: StorageContext['getDependency'] = useCallback(
        async ({ diagramId, id }) => {
            const diagram = await fetchDiagram(diagramId);
            return diagram.dependencies?.find((dependency) => dependency.id === id);
        },
        [fetchDiagram]
    );

    const updateDependency: StorageContext['updateDependency'] = useCallback(
        async ({ id, attributes }) => {
            const diagramId = await resolveDiagramIdByEntity(
                id,
                dependencyOwnersRef,
                'dependencies'
            );
            await mutateDiagram(diagramId, (diagram) => {
                diagram.dependencies = (diagram.dependencies ?? []).map(
                    (dependency) =>
                        dependency.id === id
                            ? { ...dependency, ...attributes }
                            : dependency
                );
            });
        },
        [mutateDiagram, resolveDiagramIdByEntity]
    );

    const deleteDependency: StorageContext['deleteDependency'] = useCallback(
        async ({ diagramId, id }) => {
            await mutateDiagram(diagramId, (diagram) => {
                diagram.dependencies = (diagram.dependencies ?? []).filter(
                    (dependency) => dependency.id !== id
                );
            });
        },
        [mutateDiagram]
    );

    const listDependencies: StorageContext['listDependencies'] = useCallback(
        async (diagramId) => {
            const diagram = await fetchDiagram(diagramId);
            return diagram.dependencies ?? [];
        },
        [fetchDiagram]
    );

    const deleteDiagramDependencies: StorageContext['deleteDiagramDependencies'] =
        useCallback(
            async (diagramId) => {
                await mutateDiagram(diagramId, (diagram) => {
                    diagram.dependencies = [];
                });
            },
            [mutateDiagram]
        );

    const addArea: StorageContext['addArea'] = useCallback(
        async ({ diagramId, area }) => {
            await mutateDiagram(diagramId, (diagram) => {
                const areas = diagram.areas ?? [];
                diagram.areas = [...areas, area];
            });
        },
        [mutateDiagram]
    );

    const getArea: StorageContext['getArea'] = useCallback(
        async ({ diagramId, id }) => {
            const diagram = await fetchDiagram(diagramId);
            return diagram.areas?.find((area) => area.id === id);
        },
        [fetchDiagram]
    );

    const updateArea: StorageContext['updateArea'] = useCallback(
        async ({ id, attributes }) => {
            const diagramId = await resolveDiagramIdByEntity(
                id,
                areaOwnersRef,
                'areas'
            );
            await mutateDiagram(diagramId, (diagram) => {
                diagram.areas = (diagram.areas ?? []).map((area) =>
                    area.id === id ? { ...area, ...attributes } : area
                );
            });
        },
        [mutateDiagram, resolveDiagramIdByEntity]
    );

    const deleteArea: StorageContext['deleteArea'] = useCallback(
        async ({ diagramId, id }) => {
            await mutateDiagram(diagramId, (diagram) => {
                diagram.areas = (diagram.areas ?? []).filter(
                    (area) => area.id !== id
                );
            });
        },
        [mutateDiagram]
    );

    const listAreas: StorageContext['listAreas'] = useCallback(
        async (diagramId) => {
            const diagram = await fetchDiagram(diagramId);
            return diagram.areas ?? [];
        },
        [fetchDiagram]
    );

    const deleteDiagramAreas: StorageContext['deleteDiagramAreas'] = useCallback(
        async (diagramId) => {
            await mutateDiagram(diagramId, (diagram) => {
                diagram.areas = [];
            });
        },
        [mutateDiagram]
    );

    const addCustomType: StorageContext['addCustomType'] = useCallback(
        async ({ diagramId, customType }) => {
            await mutateDiagram(diagramId, (diagram) => {
                const customTypes = diagram.customTypes ?? [];
                diagram.customTypes = [...customTypes, customType];
            });
        },
        [mutateDiagram]
    );

    const getCustomType: StorageContext['getCustomType'] = useCallback(
        async ({ diagramId, id }) => {
            const diagram = await fetchDiagram(diagramId);
            return diagram.customTypes?.find((customType) => customType.id === id);
        },
        [fetchDiagram]
    );

    const updateCustomType: StorageContext['updateCustomType'] = useCallback(
        async ({ id, attributes }) => {
            const diagramId = await resolveDiagramIdByEntity(
                id,
                customTypeOwnersRef,
                'customTypes'
            );
            await mutateDiagram(diagramId, (diagram) => {
                diagram.customTypes = (diagram.customTypes ?? []).map(
                    (customType) =>
                        customType.id === id
                            ? { ...customType, ...attributes }
                            : customType
                );
            });
        },
        [mutateDiagram, resolveDiagramIdByEntity]
    );

    const deleteCustomType: StorageContext['deleteCustomType'] = useCallback(
        async ({ diagramId, id }) => {
            await mutateDiagram(diagramId, (diagram) => {
                diagram.customTypes = (diagram.customTypes ?? []).filter(
                    (customType) => customType.id !== id
                );
            });
        },
        [mutateDiagram]
    );

    const listCustomTypes: StorageContext['listCustomTypes'] = useCallback(
        async (diagramId) => {
            const diagram = await fetchDiagram(diagramId);
            return [...(diagram.customTypes ?? [])].sort((a, b) =>
                a.name.localeCompare(b.name)
            );
        },
        [fetchDiagram]
    );

    const deleteDiagramCustomTypes: StorageContext['deleteDiagramCustomTypes'] =
        useCallback(
            async (diagramId) => {
                await mutateDiagram(diagramId, (diagram) => {
                    diagram.customTypes = [];
                });
            },
            [mutateDiagram]
        );

    const addNote: StorageContext['addNote'] = useCallback(
        async ({ diagramId, note }) => {
            await mutateDiagram(diagramId, (diagram) => {
                const notes = diagram.notes ?? [];
                diagram.notes = [...notes, note];
            });
        },
        [mutateDiagram]
    );

    const getNote: StorageContext['getNote'] = useCallback(
        async ({ diagramId, id }) => {
            const diagram = await fetchDiagram(diagramId);
            return diagram.notes?.find((note) => note.id === id);
        },
        [fetchDiagram]
    );

    const updateNote: StorageContext['updateNote'] = useCallback(
        async ({ id, attributes }) => {
            const diagramId = await resolveDiagramIdByEntity(
                id,
                noteOwnersRef,
                'notes'
            );
            await mutateDiagram(diagramId, (diagram) => {
                diagram.notes = (diagram.notes ?? []).map((note) =>
                    note.id === id ? { ...note, ...attributes } : note
                );
            });
        },
        [mutateDiagram, resolveDiagramIdByEntity]
    );

    const deleteNote: StorageContext['deleteNote'] = useCallback(
        async ({ diagramId, id }) => {
            await mutateDiagram(diagramId, (diagram) => {
                diagram.notes = (diagram.notes ?? []).filter(
                    (note) => note.id !== id
                );
            });
        },
        [mutateDiagram]
    );

    const listNotes: StorageContext['listNotes'] = useCallback(
        async (diagramId) => {
            const diagram = await fetchDiagram(diagramId);
            return diagram.notes ?? [];
        },
        [fetchDiagram]
    );

    const deleteDiagramNotes: StorageContext['deleteDiagramNotes'] = useCallback(
        async (diagramId) => {
            await mutateDiagram(diagramId, (diagram) => {
                diagram.notes = [];
            });
        },
        [mutateDiagram]
    );

    const value = useMemo<StorageContext>(
        () => ({
            getConfig,
            updateConfig,
            getDiagramFilter,
            updateDiagramFilter,
            deleteDiagramFilter,
            addDiagram,
            listDiagrams,
            getDiagram,
            updateDiagram,
            deleteDiagram,
            addTable,
            getTable,
            updateTable,
            putTable,
            deleteTable,
            listTables,
            deleteDiagramTables,
            addRelationship,
            getRelationship,
            updateRelationship,
            deleteRelationship,
            listRelationships,
            deleteDiagramRelationships,
            addDependency,
            getDependency,
            updateDependency,
            deleteDependency,
            listDependencies,
            deleteDiagramDependencies,
            addArea,
            getArea,
            updateArea,
            deleteArea,
            listAreas,
            deleteDiagramAreas,
            addCustomType,
            getCustomType,
            updateCustomType,
            deleteCustomType,
            listCustomTypes,
            deleteDiagramCustomTypes,
            addNote,
            getNote,
            updateNote,
            deleteNote,
            listNotes,
            deleteDiagramNotes,
        }),
        [
            addArea,
            addCustomType,
            addDependency,
            addDiagram,
            addNote,
            addRelationship,
            addTable,
            deleteArea,
            deleteCustomType,
            deleteDependency,
            deleteDiagram,
            deleteDiagramAreas,
            deleteDiagramCustomTypes,
            deleteDiagramDependencies,
            deleteDiagramFilter,
            deleteDiagramNotes,
            deleteDiagramRelationships,
            deleteDiagramTables,
            deleteNote,
            deleteRelationship,
            deleteTable,
            getArea,
            getConfig,
            getCustomType,
            getDependency,
            getDiagram,
            getDiagramFilter,
            getNote,
            getRelationship,
            getTable,
            listAreas,
            listCustomTypes,
            listDependencies,
            listDiagrams,
            listNotes,
            listRelationships,
            listTables,
            putTable,
            updateArea,
            updateConfig,
            updateCustomType,
            updateDependency,
            updateDiagram,
            updateDiagramFilter,
            updateNote,
            updateRelationship,
            updateTable,
        ]
    );

    return (
        <storageContext.Provider value={value}>{children}</storageContext.Provider>
    );
};
