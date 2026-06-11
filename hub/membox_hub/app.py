from fastapi import FastAPI

from hub.membox_hub.api import ingest, search, pdf


def create_app() -> FastAPI:
    app = FastAPI(title="membox API", version="0.5")
    app.include_router(ingest.router)
    app.include_router(search.router)
    app.include_router(pdf.router)
    return app


app = create_app()
